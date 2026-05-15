package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/logging"
	"kube-insight/internal/resourceprofile"
	storagesql "kube-insight/internal/storage/sql"

	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	path         string
	profileRules []resourceprofile.Rule
}

type Options struct {
	ProfileRules []resourceprofile.Rule
}

func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{})
}

func OpenWithOptions(path string, opts Options) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	profileRules := opts.ProfileRules
	if len(profileRules) == 0 {
		profileRules = resourceprofile.DefaultRules()
	} else {
		profileRules = resourceprofile.CloneRules(profileRules)
	}
	s := &Store{db: db, path: path, profileRules: profileRules}
	if err := s.bootstrap(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) profileForResource(info kubeapi.ResourceInfo) resourceprofile.Profile {
	return resourceprofile.Select(info, s.profileRules)
}

func (s *Store) bootstrap(ctx context.Context) error {
	for _, stmt := range []string{
		"pragma foreign_keys = on",
		"pragma auto_vacuum = incremental",
		"pragma journal_mode = wal",
		"pragma journal_size_limit = 67108864",
		"pragma busy_timeout = 5000",
		storagesql.Schema,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return s.migrate(ctx)
}

func (s *Store) PutObservation(ctx context.Context, obs core.Observation, evidence extractor.Evidence) error {
	logger := logging.FromContext(ctx).With("component", "storage")
	if obs.Ref.Name == "" {
		return errors.New("observation ref name is required")
	}

	doc, err := json.Marshal(obs.Object)
	if err != nil {
		return err
	}
	digest := digest(doc)
	observedAt := millis(obs.ObservedAt)
	deletedAt := sql.NullInt64{}
	if obs.Type == core.ObservationDeleted {
		deletedAt = sql.NullInt64{Int64: observedAt, Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	clusterID, err := ensureCluster(ctx, tx, obs.Ref.ClusterID, observedAt)
	if err != nil {
		return err
	}
	apiResourceID, kindID, err := ensureKind(ctx, tx, obs.Ref, observedAt, s.profileForResource)
	if err != nil {
		return err
	}
	_ = apiResourceID

	objectRecord, err := ensureObjectRecord(ctx, tx, clusterID, kindID, obs.Ref, observedAt, deletedAt)
	if err != nil {
		return err
	}
	objectID := objectRecord.ID
	latestVersionID, latestDigest, err := latestContentState(ctx, tx, objectID)
	if err != nil {
		return err
	}
	if shouldSkipUnchangedObservation(obs, objectRecord, latestVersionID, latestDigest, digest) {
		if err := updateLatestSeen(ctx, tx, objectID, observedAt); err != nil {
			return err
		}
		if err := putObservationTrace(ctx, tx, clusterID, objectID, obs, latestVersionID, false); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logger.Debug("skipped unchanged observation", "cluster", obs.Ref.ClusterID, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name, "type", obs.Type, "versionId", latestVersionID, "bytes", len(doc))
		return nil
	}
	if err := putBlob(ctx, tx, digest, doc); err != nil {
		return err
	}
	seq, err := nextSequence(ctx, tx, objectID)
	if err != nil {
		return err
	}
	versionID, err := putVersion(ctx, tx, objectID, seq, observedAt, obs.ResourceVersion, generation(obs.Object), digest, len(doc))
	if err != nil {
		return err
	}
	if err := updateLatest(ctx, tx, objectID, clusterID, kindID, obs.Ref, versionID, observedAt, string(doc)); err != nil {
		return err
	}
	if err := putObservationTrace(ctx, tx, clusterID, objectID, obs, versionID, true); err != nil {
		return err
	}
	if err := putFacts(ctx, tx, clusterID, kindID, objectID, versionID, obs.Ref, evidence.Facts); err != nil {
		return err
	}
	if err := putChanges(ctx, tx, clusterID, objectID, versionID, evidence.Changes); err != nil {
		return err
	}
	if err := s.putEdges(ctx, tx, clusterID, objectID, versionID, obs, evidence.Edges); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	logger.Debug("stored observation", "cluster", obs.Ref.ClusterID, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name, "type", obs.Type, "versionId", versionID, "facts", len(evidence.Facts), "edges", len(evidence.Edges), "changes", len(evidence.Changes), "bytes", len(doc))
	return nil
}

type objectRecord struct {
	ID         int64
	Existed    bool
	WasDeleted bool
}

func (s *Store) GetFacts(ctx context.Context, objectID string) ([]core.Fact, error) {
	dbObjectID, err := s.lookupObjectID(ctx, objectID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
select ts, fact_key, fact_value, numeric_value, severity, detail
from object_facts
where object_id = ?
order by ts, id`, dbObjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []core.Fact
	for rows.Next() {
		var ts int64
		var key string
		var value sql.NullString
		var numeric sql.NullFloat64
		var severity int
		var detailText sql.NullString
		if err := rows.Scan(&ts, &key, &value, &numeric, &severity, &detailText); err != nil {
			return nil, err
		}
		f := core.Fact{
			Time:     time.UnixMilli(ts),
			ObjectID: objectID,
			Key:      key,
			Value:    value.String,
			Severity: severity,
		}
		if numeric.Valid {
			v := numeric.Float64
			f.NumericValue = &v
		}
		if detailText.Valid && detailText.String != "" {
			_ = json.Unmarshal([]byte(detailText.String), &f.Detail)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func (s *Store) GetEdges(ctx context.Context, sourceID string) ([]core.Edge, error) {
	dbSourceID, err := s.lookupObjectID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
select e.edge_type, dst.namespace, dst.name, ar.api_group, ar.resource, e.valid_from, e.valid_to, e.detail
from object_edges e
join objects dst on dst.id = e.dst_id
join object_kinds ok on ok.id = dst.kind_id
join api_resources ar on ar.id = ok.api_resource_id
where e.src_id = ?
order by e.valid_from, e.id`, dbSourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cluster, _, _ := splitObjectID(sourceID)
	var edges []core.Edge
	for rows.Next() {
		var edgeType string
		var namespace sql.NullString
		var name string
		var group string
		var resource string
		var validFrom int64
		var validTo int64
		var detailText sql.NullString
		if err := rows.Scan(&edgeType, &namespace, &name, &group, &resource, &validFrom, &validTo, &detailText); err != nil {
			return nil, err
		}
		prefix := resource
		if group != "" {
			prefix = group + "/" + resource
		}
		target := prefix + "/" + name
		if namespace.Valid && namespace.String != "" {
			target = prefix + "/" + namespace.String + "/" + name
		}
		e := core.Edge{
			Type:      edgeType,
			SourceID:  sourceID,
			TargetID:  cluster + "/" + target,
			ValidFrom: time.UnixMilli(validFrom),
		}
		if validTo != math.MaxInt64 {
			t := time.UnixMilli(validTo)
			e.ValidTo = &t
		}
		if detailText.Valid && detailText.String != "" {
			_ = json.Unmarshal([]byte(detailText.String), &e.Detail)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (s *Store) lookupObjectID(ctx context.Context, logicalID string) (int64, error) {
	clusterName, rest, ok := splitObjectID(logicalID)
	if !ok {
		return 0, fmt.Errorf("invalid object id %q", logicalID)
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
select o.id
from objects o
join clusters c on c.id = o.cluster_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
where c.name = ?
  and (
    (o.uid = ? and o.uid is not null and o.uid <> '')
    or case
      when o.namespace is null or o.namespace = '' then ar.api_group || '/' || ar.resource || '/' || o.name
      else ar.api_group || '/' || ar.resource || '/' || o.namespace || '/' || o.name
    end = ?
    or case
      when o.namespace is null or o.namespace = '' then ar.resource || '/' || o.name
      else ar.resource || '/' || o.namespace || '/' || o.name
    end = ?
  )
order by case when o.uid = ? then 0 else 1 end, o.id
limit 1`, clusterName, rest, rest, rest, rest).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("object %q not found", logicalID)
		}
		return 0, err
	}
	return id, nil
}

func ensureCluster(ctx context.Context, tx *sql.Tx, name string, now int64) (int64, error) {
	if name == "" {
		name = "local"
	}
	if _, err := tx.ExecContext(ctx, `
insert into clusters(name, created_at)
values(?, ?)
on conflict(name) do nothing`, name, now); err != nil {
		return 0, err
	}
	var id int64
	return id, tx.QueryRowContext(ctx, `select id from clusters where name = ?`, name).Scan(&id)
}

func ensureKind(ctx context.Context, tx *sql.Tx, ref core.ResourceRef, now int64, profileFor func(kubeapi.ResourceInfo) resourceprofile.Profile) (int64, int64, error) {
	namespaced := ref.Namespace != ""
	if _, err := tx.ExecContext(ctx, `
insert into api_resources(api_group, api_version, resource, kind, namespaced, verbs, last_discovered_at)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(api_group, api_version, resource) do update set
  kind = excluded.kind,
  namespaced = excluded.namespaced,
  last_discovered_at = excluded.last_discovered_at`,
		ref.Group, ref.Version, ref.Resource, ref.Kind, namespaced, "[]", now); err != nil {
		return 0, 0, err
	}

	var apiResourceID int64
	if err := tx.QueryRowContext(ctx, `
select id from api_resources
where api_group = ? and api_version = ? and resource = ?`,
		ref.Group, ref.Version, ref.Resource).Scan(&apiResourceID); err != nil {
		return 0, 0, err
	}
	info := kubeapi.ResourceInfo{
		Group:      ref.Group,
		Version:    ref.Version,
		Resource:   ref.Resource,
		Kind:       ref.Kind,
		Namespaced: namespaced,
	}
	profile := resourceprofile.ForResource(info)
	if profileFor != nil {
		profile = profileFor(info)
	}
	if err := upsertProcessingProfile(ctx, tx, apiResourceID, profile); err != nil {
		return 0, 0, err
	}

	if _, err := tx.ExecContext(ctx, `
insert into object_kinds(api_resource_id, api_group, api_version, kind)
values(?, ?, ?, ?)
on conflict(api_group, api_version, kind) do update set
  api_resource_id = excluded.api_resource_id`,
		apiResourceID, ref.Group, ref.Version, ref.Kind); err != nil {
		return 0, 0, err
	}

	var kindID int64
	if err := tx.QueryRowContext(ctx, `
select id from object_kinds
where api_group = ? and api_version = ? and kind = ?`,
		ref.Group, ref.Version, ref.Kind).Scan(&kindID); err != nil {
		return 0, 0, err
	}
	return apiResourceID, kindID, nil
}

func ensureObject(ctx context.Context, tx *sql.Tx, clusterID, kindID int64, ref core.ResourceRef, now int64, deletedAt sql.NullInt64) (int64, error) {
	record, err := ensureObjectRecord(ctx, tx, clusterID, kindID, ref, now, deletedAt)
	return record.ID, err
}

func ensureObjectRecord(ctx context.Context, tx *sql.Tx, clusterID, kindID int64, ref core.ResourceRef, now int64, deletedAt sql.NullInt64) (objectRecord, error) {
	var id int64
	var previousDeletedAt sql.NullInt64
	var err error
	if ref.UID != "" {
		err = tx.QueryRowContext(ctx, `
select id, deleted_at from objects
where cluster_id = ? and kind_id = ? and uid = ?
order by id
limit 1`, clusterID, kindID, ref.UID).Scan(&id, &previousDeletedAt)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `
select id, deleted_at from objects
where cluster_id = ? and kind_id = ? and coalesce(namespace, '') = ? and name = ? and coalesce(uid, '') = ''
order by id
limit 1`, clusterID, kindID, ref.Namespace, ref.Name).Scan(&id, &previousDeletedAt)
		}
	} else {
		err = tx.QueryRowContext(ctx, `
select id, deleted_at from objects
where cluster_id = ? and kind_id = ? and coalesce(namespace, '') = ? and name = ?
order by case when coalesce(uid, '') <> '' then 0 else 1 end, id
limit 1`, clusterID, kindID, ref.Namespace, ref.Name).Scan(&id, &previousDeletedAt)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `
update objects
set last_seen_at = ?, uid = coalesce(?, uid), latest_version_id = latest_version_id, deleted_at = ?
where id = ?`, now, nullable(ref.UID), deletedAt, id)
		return objectRecord{ID: id, Existed: true, WasDeleted: previousDeletedAt.Valid}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return objectRecord{}, err
	}

	res, err := tx.ExecContext(ctx, `
insert into objects(cluster_id, kind_id, namespace, name, uid, first_seen_at, last_seen_at, deleted_at)
values(?, ?, ?, ?, ?, ?, ?, ?)`,
		clusterID, kindID, nullable(ref.Namespace), ref.Name, nullable(ref.UID), now, now, deletedAt)
	if err != nil {
		return objectRecord{}, err
	}
	id, err = res.LastInsertId()
	return objectRecord{ID: id}, err
}

func putBlob(ctx context.Context, tx *sql.Tx, digest string, doc []byte) error {
	_, err := tx.ExecContext(ctx, `
insert into blobs(digest, codec, raw_size, stored_size, data)
values(?, 'identity', ?, ?, ?)
on conflict(digest) do nothing`, digest, len(doc), len(doc), doc)
	return err
}

func latestContentState(ctx context.Context, tx *sql.Tx, objectID int64) (int64, string, error) {
	var versionID int64
	var docHash string
	err := tx.QueryRowContext(ctx, `
select v.id, v.doc_hash
from objects o
join versions v on v.id = o.latest_version_id
where o.id = ?`, objectID).Scan(&versionID, &docHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return versionID, docHash, err
}

func shouldSkipUnchangedObservation(obs core.Observation, object objectRecord, latestVersionID int64, latestDigest, nextDigest string) bool {
	if obs.Type == core.ObservationDeleted {
		return false
	}
	if !object.Existed || object.WasDeleted || latestVersionID == 0 {
		return false
	}
	return latestDigest == nextDigest
}

func updateLatestSeen(ctx context.Context, tx *sql.Tx, objectID, observedAt int64) error {
	_, err := tx.ExecContext(ctx, `
update latest_index
set observed_at = ?
where object_id = ?`, observedAt, objectID)
	return err
}

func nextSequence(ctx context.Context, tx *sql.Tx, objectID int64) (int64, error) {
	var seq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `select max(seq) from versions where object_id = ?`, objectID).Scan(&seq); err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 1, nil
	}
	return seq.Int64 + 1, nil
}

func putVersion(ctx context.Context, tx *sql.Tx, objectID, seq, observedAt int64, resourceVersion string, generation sql.NullInt64, digest string, size int) (int64, error) {
	res, err := tx.ExecContext(ctx, `
insert into versions(
  object_id, seq, observed_at, resource_version, generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
)
values(?, ?, ?, ?, ?, ?, 'full', 'full_identity', ?, ?, ?, 0, '{}')`,
		objectID, seq, observedAt, nullable(resourceVersion), generation, digest, digest, size, size)
	if err != nil {
		return 0, err
	}
	versionID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `update objects set latest_version_id = ? where id = ?`, versionID, objectID)
	return versionID, err
}

func updateLatest(ctx context.Context, tx *sql.Tx, objectID, clusterID, kindID int64, ref core.ResourceRef, versionID, observedAt int64, doc string) error {
	hasDoc, err := latestIndexHasDocColumn(ctx, tx)
	if err != nil {
		return err
	}
	if hasDoc {
		_, err = tx.ExecContext(ctx, `
insert into latest_index(object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at, doc)
values(?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(object_id) do update set
  cluster_id = excluded.cluster_id,
  kind_id = excluded.kind_id,
  namespace = excluded.namespace,
  name = excluded.name,
  uid = excluded.uid,
  latest_version_id = excluded.latest_version_id,
  observed_at = excluded.observed_at,
  doc = excluded.doc`,
			objectID, clusterID, kindID, nullable(ref.Namespace), ref.Name, nullable(ref.UID), versionID, observedAt, doc)
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into latest_index(object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at)
values(?, ?, ?, ?, ?, ?, ?, ?)
on conflict(object_id) do update set
  cluster_id = excluded.cluster_id,
  kind_id = excluded.kind_id,
  namespace = excluded.namespace,
  name = excluded.name,
  uid = excluded.uid,
  latest_version_id = excluded.latest_version_id,
  observed_at = excluded.observed_at`,
		objectID, clusterID, kindID, nullable(ref.Namespace), ref.Name, nullable(ref.UID), versionID, observedAt)
	return err
}

func latestIndexHasDocColumn(ctx context.Context, tx *sql.Tx) (bool, error) {
	rows, err := tx.QueryContext(ctx, `pragma table_info(latest_index)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == "doc" {
			return true, nil
		}
	}
	return false, rows.Err()
}

func putFacts(ctx context.Context, tx *sql.Tx, clusterID, kindID, objectID, versionID int64, ref core.ResourceRef, facts []core.Fact) error {
	for _, f := range facts {
		detail, err := marshalNullable(f.Detail)
		if err != nil {
			return err
		}
		var numeric any
		if f.NumericValue != nil {
			numeric = *f.NumericValue
		}
		if _, err := tx.ExecContext(ctx, `
insert into object_facts(
  cluster_id, ts, object_id, version_id, kind_id, namespace, name,
  fact_key, fact_value, numeric_value, severity, detail
)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			clusterID, millis(f.Time), objectID, versionID, kindID, nullable(ref.Namespace), nullable(ref.Name),
			f.Key, nullable(f.Value), numeric, f.Severity, detail); err != nil {
			return err
		}
	}
	return nil
}

func putChanges(ctx context.Context, tx *sql.Tx, clusterID, objectID, versionID int64, changes []core.Change) error {
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `
insert into object_changes(cluster_id, ts, object_id, version_id, change_family, path, op, old_scalar, new_scalar, severity)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			clusterID, millis(c.Time), objectID, versionID, c.Family, c.Path, c.Op, nullable(c.Old), nullable(c.New), c.Severity); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) putEdges(ctx context.Context, tx *sql.Tx, clusterID, sourceObjectID, versionID int64, obs core.Observation, edges []core.Edge) error {
	for _, e := range edges {
		targetID, err := s.ensureEdgeTarget(ctx, tx, clusterID, obs, e.TargetID)
		if err != nil {
			return err
		}
		detail, err := marshalNullable(e.Detail)
		if err != nil {
			return err
		}
		validTo := int64(math.MaxInt64)
		if e.ValidTo != nil {
			validTo = millis(*e.ValidTo)
		}
		if _, err := tx.ExecContext(ctx, `
insert into object_edges(cluster_id, edge_type, src_id, dst_id, valid_from, valid_to, src_version_id, confidence, detail)
values(?, ?, ?, ?, ?, ?, ?, 100, ?)`,
			clusterID, e.Type, sourceObjectID, targetID, millis(e.ValidFrom), validTo, versionID, detail); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureEdgeTarget(ctx context.Context, tx *sql.Tx, clusterID int64, obs core.Observation, targetID string) (int64, error) {
	clusterName, rest, ok := splitObjectID(targetID)
	if ok && clusterName != obs.Ref.ClusterID {
		return 0, fmt.Errorf("cross-cluster edge target %q is not supported", targetID)
	}
	if ok {
		targetID = rest
	}

	ref, err := targetRef(ctx, tx, obs.Ref.ClusterID, obs.Ref.Version, obs.Ref.Namespace, targetID)
	if err != nil {
		return 0, err
	}
	if _, kindID, err := ensureKind(ctx, tx, ref, millis(obs.ObservedAt), s.profileForResource); err != nil {
		return 0, err
	} else {
		return ensureObject(ctx, tx, clusterID, kindID, ref, millis(obs.ObservedAt), sql.NullInt64{})
	}
}

func targetRef(ctx context.Context, tx *sql.Tx, clusterID, version, defaultNamespace, target string) (core.ResourceRef, error) {
	parts := strings.Split(target, "/")
	if len(parts) < 2 {
		return core.ResourceRef{}, fmt.Errorf("invalid edge target %q", target)
	}
	group := ""
	resource := parts[0]
	namespace := ""
	name := parts[1]
	if strings.Contains(parts[0], ".") && len(parts) >= 3 {
		group = parts[0]
		resource = parts[1]
		info, ok := lookupAPIResource(ctx, tx, group, resource)
		if !ok {
			resolver := kubeapi.NewResolver()
			info, _ = resolver.ResolveGVR(group, version, resource)
		}
		if info.Namespaced && len(parts) >= 4 {
			namespace = parts[2]
			name = strings.Join(parts[3:], "/")
		} else {
			name = strings.Join(parts[2:], "/")
		}
		return core.ResourceRef{
			ClusterID: clusterID,
			Group:     info.Group,
			Version:   info.Version,
			Resource:  info.Resource,
			Kind:      info.Kind,
			Namespace: namespace,
			Name:      name,
		}, nil
	}
	info, ok := lookupAPIResource(ctx, tx, group, resource)
	if !ok {
		resolver := kubeapi.NewResolver()
		info, _ = resolver.ResolveGVR(group, version, resource)
	}
	if info.Namespaced && len(parts) >= 3 {
		namespace = parts[1]
		name = strings.Join(parts[2:], "/")
	} else if len(parts) > 2 {
		name = strings.Join(parts[1:], "/")
	}
	if namespace == "" && info.Namespaced {
		namespace = defaultNamespace
	}
	return core.ResourceRef{
		ClusterID: clusterID,
		Group:     info.Group,
		Version:   info.Version,
		Resource:  info.Resource,
		Kind:      info.Kind,
		Namespace: namespace,
		Name:      name,
	}, nil
}

func lookupAPIResource(ctx context.Context, tx *sql.Tx, group, resource string) (kubeapi.ResourceInfo, bool) {
	var info kubeapi.ResourceInfo
	err := tx.QueryRowContext(ctx, `
select api_group, api_version, resource, kind, namespaced
from api_resources
where api_group = ? and resource = ?
order by case when preferred_version = api_version then 0 else 1 end,
         case when storage_version = api_version then 0 else 1 end,
         id desc
limit 1`, group, resource).Scan(&info.Group, &info.Version, &info.Resource, &info.Kind, &info.Namespaced)
	if err != nil {
		return kubeapi.ResourceInfo{}, false
	}
	return info, true
}

func generation(obj map[string]any) sql.NullInt64 {
	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return sql.NullInt64{}
	}
	switch v := metadata["generation"].(type) {
	case int:
		return sql.NullInt64{Int64: int64(v), Valid: true}
	case int64:
		return sql.NullInt64{Int64: v, Valid: true}
	case float64:
		return sql.NullInt64{Int64: int64(v), Valid: true}
	case json.Number:
		i, err := strconv.ParseInt(v.String(), 10, 64)
		return sql.NullInt64{Int64: i, Valid: err == nil}
	default:
		return sql.NullInt64{}
	}
}

func digest(doc []byte) string {
	sum := sha256.Sum256(doc)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func marshalNullable(value map[string]any) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(out), nil
}

func splitObjectID(value string) (string, string, bool) {
	i := strings.IndexByte(value, '/')
	if i <= 0 || i == len(value)-1 {
		return "", "", false
	}
	return value[:i], value[i+1:], true
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
