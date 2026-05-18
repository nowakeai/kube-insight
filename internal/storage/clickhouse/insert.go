package clickhouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kube-insight/internal/core"
)

type EvidenceBatch struct {
	Database      string           `json:"database"`
	Observations  []map[string]any `json:"observations"`
	ObjectAliases []map[string]any `json:"objectAliases"`
	Versions      []map[string]any `json:"versions"`
	Facts         []map[string]any `json:"facts"`
	Edges         []map[string]any `json:"edges"`
	Changes       []map[string]any `json:"changes"`
}

type InsertResult struct {
	Endpoint string         `json:"endpoint"`
	Database string         `json:"database"`
	Tables   map[string]int `json:"tables"`
	Rows     int            `json:"rows"`
}

type evidenceBatchContext struct {
	Batch   EvidenceBatch
	Refs    map[string]core.ResourceRef
	Aliases map[string]string
	Changed []bool
}

func BuildEvidenceBatch(database string, observations []core.Observation, facts []core.Fact, edges []core.Edge, changes []core.Change) (EvidenceBatch, error) {
	pending := make([]pendingObservation, 0, len(observations))
	for _, obs := range observations {
		pending = append(pending, pendingObservation{Observation: obs})
	}
	built, err := buildObservationRows(database, pending, nil)
	if err != nil {
		return EvidenceBatch{}, err
	}
	if err := appendFactRows(&built.Batch, built.Refs, built.Aliases, facts); err != nil {
		return EvidenceBatch{}, err
	}
	if err := appendEdgeRows(&built.Batch, built.Refs, built.Aliases, edges); err != nil {
		return EvidenceBatch{}, err
	}
	appendChangeRows(&built.Batch, built.Refs, built.Aliases, changes)
	return built.Batch, nil
}

func buildEvidenceBatchForPending(database string, observations []pendingObservation, states map[string]objectVersionState) (EvidenceBatch, error) {
	built, err := buildObservationRows(database, observations, states)
	if err != nil {
		return EvidenceBatch{}, err
	}
	for i, pending := range observations {
		if !built.Changed[i] {
			continue
		}
		if err := appendFactRows(&built.Batch, built.Refs, built.Aliases, pending.Evidence.Facts); err != nil {
			return EvidenceBatch{}, err
		}
		if err := appendEdgeRows(&built.Batch, built.Refs, built.Aliases, pending.Evidence.Edges); err != nil {
			return EvidenceBatch{}, err
		}
		appendChangeRows(&built.Batch, built.Refs, built.Aliases, pending.Evidence.Changes)
	}
	return built.Batch, nil
}

func buildObservationRows(database string, observations []pendingObservation, states map[string]objectVersionState) (evidenceBatchContext, error) {
	if database == "" {
		database = defaultDatabase
	}
	built := evidenceBatchContext{
		Batch:   EvidenceBatch{Database: database},
		Refs:    map[string]core.ResourceRef{},
		Aliases: map[string]string{},
		Changed: make([]bool, len(observations)),
	}
	localStates := cloneVersionStates(states)
	for _, pending := range observations {
		objectID := logicalID(pending.Observation.Ref)
		registerObjectAliases(built.Refs, built.Aliases, pending.Observation.Ref, objectID)
	}
	for i, pending := range observations {
		obs := pending.Observation
		objectID := logicalID(obs.Ref)
		for _, aliasID := range objectAliasIDs(obs.Ref, objectID) {
			built.Batch.ObjectAliases = append(built.Batch.ObjectAliases, map[string]any{
				"cluster_id":  obs.Ref.ClusterID,
				"object_id":   objectID,
				"alias_id":    aliasID,
				"kind":        obs.Ref.Kind,
				"namespace":   obs.Ref.Namespace,
				"name":        obs.Ref.Name,
				"observed_at": clickHouseTime(obs.ObservedAt),
			})
		}
		docBytes, err := json.Marshal(obs.Object)
		if err != nil {
			return evidenceBatchContext{}, err
		}
		digest := sha256Digest(docBytes)
		built.Batch.Observations = append(built.Batch.Observations, map[string]any{
			"cluster_id":       obs.Ref.ClusterID,
			"observed_at":      clickHouseTime(obs.ObservedAt),
			"observation_type": string(obs.Type),
			"api_group":        obs.Ref.Group,
			"api_version":      obs.Ref.Version,
			"resource":         obs.Ref.Resource,
			"kind":             obs.Ref.Kind,
			"namespace":        obs.Ref.Namespace,
			"name":             obs.Ref.Name,
			"uid":              obs.Ref.UID,
			"resource_version": obs.ResourceVersion,
			"partial":          obs.Partial,
			"doc_hash":         digest,
			"doc":              string(docBytes),
		})
		state := localStates[objectID]
		if !observationContentChanged(obs, state, digest) {
			continue
		}
		nextSeq := state.Seq + 1
		built.Changed[i] = true
		built.Batch.Versions = append(built.Batch.Versions, map[string]any{
			"cluster_id":       obs.Ref.ClusterID,
			"object_id":        objectID,
			"seq":              nextSeq,
			"observed_at":      clickHouseTime(obs.ObservedAt),
			"api_group":        obs.Ref.Group,
			"api_version":      obs.Ref.Version,
			"resource":         obs.Ref.Resource,
			"kind":             obs.Ref.Kind,
			"namespace":        obs.Ref.Namespace,
			"name":             obs.Ref.Name,
			"uid":              obs.Ref.UID,
			"resource_version": obs.ResourceVersion,
			"doc_hash":         digest,
			"materialization":  string(core.MaterializationFull),
			"blob_ref":         digest,
			"raw_size":         len(docBytes),
			"stored_size":      len(docBytes),
			"doc":              string(docBytes),
		})
		localStates[objectID] = objectVersionState{Seq: nextSeq, DocHash: digest, Exists: true}
	}
	return built, nil
}

func cloneVersionStates(states map[string]objectVersionState) map[string]objectVersionState {
	out := make(map[string]objectVersionState, len(states))
	for objectID, state := range states {
		out[objectID] = state
	}
	return out
}

func observationContentChanged(obs core.Observation, state objectVersionState, digest string) bool {
	if obs.Type == core.ObservationDeleted {
		return true
	}
	if !state.Exists {
		return true
	}
	return state.DocHash != digest
}

func appendFactRows(batch *EvidenceBatch, refs map[string]core.ResourceRef, aliases map[string]string, facts []core.Fact) error {
	for _, fact := range facts {
		objectID := canonicalObjectID(fact.ObjectID, aliases)
		ref := refs[objectID]
		detail, err := jsonString(fact.Detail)
		if err != nil {
			return err
		}
		batch.Facts = append(batch.Facts, map[string]any{
			"cluster_id":    ref.ClusterID,
			"ts":            clickHouseTime(fact.Time),
			"object_id":     objectID,
			"kind":          ref.Kind,
			"namespace":     ref.Namespace,
			"name":          ref.Name,
			"fact_key":      fact.Key,
			"fact_value":    fact.Value,
			"numeric_value": fact.NumericValue,
			"severity":      fact.Severity,
			"detail":        detail,
		})
	}
	return nil
}

func appendEdgeRows(batch *EvidenceBatch, refs map[string]core.ResourceRef, aliases map[string]string, edges []core.Edge) error {
	for _, edge := range edges {
		srcID := canonicalObjectID(edge.SourceID, aliases)
		dstID := canonicalObjectID(edge.TargetID, aliases)
		src := refs[srcID]
		dst := refs[dstID]
		detail, err := jsonString(edge.Detail)
		if err != nil {
			return err
		}
		validTo := farFutureTime()
		validToMS := int64(9223372036854775807)
		if edge.ValidTo != nil {
			validTo = *edge.ValidTo
			validToMS = edge.ValidTo.UnixMilli()
		}
		clusterID := src.ClusterID
		if clusterID == "" {
			clusterID = dst.ClusterID
		}
		batch.Edges = append(batch.Edges, map[string]any{
			"cluster_id":    clusterID,
			"edge_type":     edge.Type,
			"src_id":        srcID,
			"dst_id":        dstID,
			"src_kind":      src.Kind,
			"dst_kind":      dst.Kind,
			"valid_from":    clickHouseTime(edge.ValidFrom),
			"valid_to":      clickHouseTime(validTo),
			"valid_from_ms": edge.ValidFrom.UnixMilli(),
			"valid_to_ms":   validToMS,
			"detail":        detail,
		})
	}
	return nil
}

func appendChangeRows(batch *EvidenceBatch, refs map[string]core.ResourceRef, aliases map[string]string, changes []core.Change) {
	for _, change := range changes {
		objectID := canonicalObjectID(change.ObjectID, aliases)
		ref := refs[objectID]
		batch.Changes = append(batch.Changes, map[string]any{
			"cluster_id":    ref.ClusterID,
			"ts":            clickHouseTime(change.Time),
			"object_id":     objectID,
			"kind":          ref.Kind,
			"namespace":     ref.Namespace,
			"name":          ref.Name,
			"change_family": change.Family,
			"path":          change.Path,
			"op":            change.Op,
			"old_scalar":    change.Old,
			"new_scalar":    change.New,
			"severity":      change.Severity,
		})
	}
}

func (c HTTPClient) InsertEvidenceBatch(ctx context.Context, batch EvidenceBatch) (InsertResult, error) {
	out := InsertResult{Endpoint: c.Endpoint, Database: batch.Database, Tables: map[string]int{}}
	for _, table := range []struct {
		name string
		rows []map[string]any
	}{
		{name: "observations", rows: batch.Observations},
		{name: "object_aliases", rows: batch.ObjectAliases},
		{name: "versions", rows: batch.Versions},
		{name: "facts", rows: batch.Facts},
		{name: "edges", rows: batch.Edges},
		{name: "changes", rows: batch.Changes},
	} {
		if len(table.rows) == 0 {
			continue
		}
		if err := c.InsertRows(ctx, batch.Database, table.name, table.rows); err != nil {
			return out, err
		}
		out.Tables[table.name] = len(table.rows)
		out.Rows += len(table.rows)
	}
	return out, nil
}

func (c HTTPClient) InsertRows(ctx context.Context, database, table string, rows []map[string]any) error {
	endpoint, err := c.insertEndpoint()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if err := (SchemaOptions{Database: database}).Validate(); err != nil {
		return err
	}
	if !identifierPattern.MatchString(table) {
		return fmt.Errorf("table %q is not a valid ClickHouse identifier", table)
	}
	var body bytes.Buffer
	body.WriteString("INSERT INTO ")
	body.WriteString(q(database))
	body.WriteString(".")
	body.WriteString(q(table))
	body.WriteString(" FORMAT JSONEachRow\n")
	encoder := json.NewEncoder(&body)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = resp.Status
		}
		return fmt.Errorf("clickhouse insert into %s failed: status=%s body=%s", table, resp.Status, message)
	}
	return nil
}

func (c HTTPClient) insertEndpoint() (string, error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("clickhouse endpoint is required")
	}
	if !c.AsyncInsert {
		return endpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	if values.Get("async_insert") == "" {
		values.Set("async_insert", "1")
	}
	if values.Get("wait_for_async_insert") == "" {
		values.Set("wait_for_async_insert", "1")
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func logicalID(ref core.ResourceRef) string {
	if ref.UID != "" {
		return ref.ClusterID + "/" + ref.UID
	}
	pathPrefix := ref.Resource
	if ref.Group != "" {
		pathPrefix = ref.Group + "/" + ref.Resource
	}
	if ref.Namespace == "" {
		return ref.ClusterID + "/" + pathPrefix + "/" + ref.Name
	}
	return ref.ClusterID + "/" + pathPrefix + "/" + ref.Namespace + "/" + ref.Name
}

func registerObjectAliases(refs map[string]core.ResourceRef, aliases map[string]string, ref core.ResourceRef, objectID string) {
	refs[objectID] = ref
	aliases[objectID] = objectID
	pathID := pathLogicalID(ref)
	if pathID != "" {
		refs[pathID] = ref
		aliases[pathID] = objectID
	}
	if ref.UID == "" && ref.ClusterID != "" {
		humanID := ref.ClusterID + "/" + ref.HumanKey()
		refs[humanID] = ref
		aliases[humanID] = objectID
	}
}

func objectAliasIDs(ref core.ResourceRef, objectID string) []string {
	seen := map[string]bool{}
	var out []string
	values := []string{objectID, pathLogicalID(ref)}
	if ref.UID == "" && ref.ClusterID != "" {
		values = append(values, ref.ClusterID+"/"+ref.HumanKey())
	}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func canonicalObjectID(value string, aliases map[string]string) string {
	if canonical := aliases[value]; canonical != "" {
		return canonical
	}
	return value
}

func pathLogicalID(ref core.ResourceRef) string {
	if ref.ClusterID == "" || ref.Resource == "" || ref.Name == "" {
		return ""
	}
	pathPrefix := ref.Resource
	if ref.Group != "" {
		pathPrefix = ref.Group + "/" + ref.Resource
	}
	if ref.Namespace == "" {
		return ref.ClusterID + "/" + pathPrefix + "/" + ref.Name
	}
	return ref.ClusterID + "/" + pathPrefix + "/" + ref.Namespace + "/" + ref.Name
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func jsonString(value map[string]any) (string, error) {
	if len(value) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func clickHouseTime(value time.Time) string {
	if value.IsZero() {
		value = time.Unix(0, 0)
	}
	return value.UTC().Format("2006-01-02 15:04:05.000")
}

func farFutureTime() time.Time {
	return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
}
