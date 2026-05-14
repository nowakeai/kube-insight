package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/logging"
)

type ObjectTarget struct {
	ClusterID string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

type ObjectRecord struct {
	LogicalID        string    `json:"logicalId"`
	ClusterID        string    `json:"clusterId"`
	Group            string    `json:"group"`
	Version          string    `json:"version"`
	Resource         string    `json:"resource"`
	Kind             string    `json:"kind"`
	Namespace        string    `json:"namespace,omitempty"`
	Name             string    `json:"name"`
	UID              string    `json:"uid,omitempty"`
	LatestObservedAt time.Time `json:"latestObservedAt"`
}

type EvidenceBundle struct {
	Object       ObjectRecord      `json:"object"`
	Latest       map[string]any    `json:"latest,omitempty"`
	Versions     []VersionEvidence `json:"versions,omitempty"`
	VersionDiffs []VersionDiff     `json:"versionDiffs,omitempty"`
	Facts        []core.Fact       `json:"facts,omitempty"`
	Edges        []core.Edge       `json:"edges,omitempty"`
	Changes      []core.Change     `json:"changes,omitempty"`
	Summary      BundleSummary     `json:"summary"`
}

type VersionEvidence struct {
	ID              int64          `json:"id"`
	Sequence        int64          `json:"sequence"`
	ObservedAt      time.Time      `json:"observedAt"`
	ResourceVersion string         `json:"resourceVersion,omitempty"`
	DocumentHash    string         `json:"documentHash"`
	Materialization string         `json:"materialization"`
	Strategy        string         `json:"strategy"`
	ReplayDepth     int            `json:"replayDepth"`
	Summary         map[string]any `json:"summary,omitempty"`
	Document        map[string]any `json:"document,omitempty"`
}

type VersionDiff struct {
	FromVersionID int64                `json:"fromVersionId"`
	ToVersionID   int64                `json:"toVersionId"`
	FromSequence  int64                `json:"fromSequence"`
	ToSequence    int64                `json:"toSequence"`
	Changes       []DocumentDiffChange `json:"changes"`
	Truncated     bool                 `json:"truncated,omitempty"`
}

type DocumentDiffChange struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type BundleSummary struct {
	Facts         int `json:"facts"`
	Edges         int `json:"edges"`
	Changes       int `json:"changes"`
	Versions      int `json:"versions,omitempty"`
	VersionDiffs  int `json:"versionDiffs,omitempty"`
	EvidenceScore int `json:"evidenceScore,omitempty"`
	Rank          int `json:"rank,omitempty"`
}

func (s *Store) Investigate(ctx context.Context, target ObjectTarget) (EvidenceBundle, error) {
	return s.InvestigateWithOptions(ctx, target, InvestigationOptions{})
}

func (s *Store) InvestigateWithOptions(ctx context.Context, target ObjectTarget, opts InvestigationOptions) (EvidenceBundle, error) {
	logger := logging.FromContext(ctx).With("component", "query")
	object, dbObjectID, err := s.FindObject(ctx, target)
	if err != nil {
		return EvidenceBundle{}, err
	}
	latest, err := s.LatestDocument(ctx, dbObjectID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	facts, err := s.GetFacts(ctx, object.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	edges, err := s.GetEdges(ctx, object.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	changes, err := s.GetChanges(ctx, object.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	bundle := EvidenceBundle{
		Object:  object,
		Latest:  latest,
		Facts:   facts,
		Edges:   edges,
		Changes: changes,
		Summary: BundleSummary{
			Facts:   len(facts),
			Edges:   len(edges),
			Changes: len(changes),
		},
	}
	if opts.MaxVersionsPerObject > 0 || hasInvestigationWindow(opts) {
		limit := opts.MaxVersionsPerObject
		if limit <= 0 {
			limit = 3
		}
		if err := s.attachBundleVersions(ctx, &bundle, opts, limit); err != nil {
			return EvidenceBundle{}, err
		}
	}
	logger.Info("investigation query completed", "cluster", object.ClusterID, "kind", object.Kind, "namespace", object.Namespace, "name", object.Name, "facts", len(facts), "edges", len(edges), "changes", len(changes), "versions", bundle.Summary.Versions)
	return bundle, nil
}

func (s *Store) FindObject(ctx context.Context, target ObjectTarget) (ObjectRecord, int64, error) {
	if target.UID == "" && (target.Kind == "" || target.Name == "") {
		return ObjectRecord{}, 0, errors.New("investigation target requires --uid or --kind and --name")
	}

	query := `
select
  o.id,
  c.name,
  ar.api_group,
  ar.api_version,
  ar.resource,
  ok.kind,
  coalesce(o.namespace, ''),
  o.name,
  coalesce(o.uid, ''),
  coalesce(li.observed_at, o.last_seen_at)
from objects o
join clusters c on c.id = o.cluster_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
left join latest_index li on li.object_id = o.id
where (? = '' or c.name = ?)
  and (
    (? <> '' and o.uid = ?)
    or (? = '' and ok.kind = ? and coalesce(o.namespace, '') = ? and o.name = ?)
  )
order by coalesce(li.observed_at, o.last_seen_at) desc, o.id desc
limit 2`

	rows, err := s.db.QueryContext(ctx, query,
		target.ClusterID, target.ClusterID,
		target.UID, target.UID,
		target.UID, target.Kind, target.Namespace, target.Name,
	)
	if err != nil {
		return ObjectRecord{}, 0, err
	}
	defer rows.Close()

	type match struct {
		record ObjectRecord
		id     int64
	}
	var matches []match
	for rows.Next() {
		var id int64
		var observedAt int64
		var record ObjectRecord
		if err := rows.Scan(
			&id,
			&record.ClusterID,
			&record.Group,
			&record.Version,
			&record.Resource,
			&record.Kind,
			&record.Namespace,
			&record.Name,
			&record.UID,
			&observedAt,
		); err != nil {
			return ObjectRecord{}, 0, err
		}
		record.LatestObservedAt = time.UnixMilli(observedAt)
		record.LogicalID = logicalID(record)
		matches = append(matches, match{record: record, id: id})
	}
	if err := rows.Err(); err != nil {
		return ObjectRecord{}, 0, err
	}
	if len(matches) == 0 {
		return ObjectRecord{}, 0, fmt.Errorf("object not found")
	}
	if len(matches) > 1 && target.ClusterID == "" {
		return ObjectRecord{}, 0, fmt.Errorf("object is ambiguous across clusters; pass --cluster")
	}
	return matches[0].record, matches[0].id, nil
}

func (s *Store) LatestObjectTarget(ctx context.Context, preferredKind string) (ObjectTarget, error) {
	row := s.db.QueryRowContext(ctx, `
select
  c.name,
  ok.kind,
  coalesce(li.namespace, ''),
  li.name,
  coalesce(li.uid, '')
from latest_index li
join clusters c on c.id = li.cluster_id
join object_kinds ok on ok.id = li.kind_id
order by case when ? <> '' and ok.kind = ? then 0 else 1 end, li.observed_at desc, li.object_id desc
limit 1`, preferredKind, preferredKind)
	var target ObjectTarget
	if err := row.Scan(&target.ClusterID, &target.Kind, &target.Namespace, &target.Name, &target.UID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ObjectTarget{}, fmt.Errorf("no objects found")
		}
		return ObjectTarget{}, err
	}
	return target, nil
}

func (s *Store) LatestDocument(ctx context.Context, dbObjectID int64) (map[string]any, error) {
	var doc string
	err := s.db.QueryRowContext(ctx, `
select cast(b.data as text)
from latest_index li
join versions v on v.id = li.latest_version_id
join blobs b on b.digest = v.blob_ref
where li.object_id = ?`, dbObjectID).Scan(&doc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("latest document not found")
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetChanges(ctx context.Context, objectID string) ([]core.Change, error) {
	dbObjectID, err := s.lookupObjectID(ctx, objectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
select ts, change_family, path, op, old_scalar, new_scalar, severity
from object_changes
where object_id = ?
order by ts, id`, dbObjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var changes []core.Change
	for rows.Next() {
		var ts int64
		var oldValue sql.NullString
		var newValue sql.NullString
		change := core.Change{ObjectID: objectID}
		if err := rows.Scan(&ts, &change.Family, &change.Path, &change.Op, &oldValue, &newValue, &change.Severity); err != nil {
			return nil, err
		}
		change.Time = time.UnixMilli(ts)
		change.Old = oldValue.String
		change.New = newValue.String
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func logicalID(record ObjectRecord) string {
	if record.UID != "" {
		return record.ClusterID + "/" + record.UID
	}
	pathPrefix := record.Resource
	if record.Group != "" {
		pathPrefix = record.Group + "/" + record.Resource
	}
	if record.Namespace == "" {
		return record.ClusterID + "/" + pathPrefix + "/" + record.Name
	}
	return record.ClusterID + "/" + pathPrefix + "/" + record.Namespace + "/" + record.Name
}
