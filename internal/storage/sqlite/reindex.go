package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/logging"
)

type EvidenceFunc func(context.Context, BackfillVersion) (extractor.Evidence, error)

type ReindexOptions struct {
	DryRun       bool
	BatchObjects int
	Evidence     EvidenceFunc
}

type ReindexReport struct {
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	DryRun          bool      `json:"dryRun"`
	Batches         int64     `json:"batches"`
	Objects         int64     `json:"objects"`
	VersionsScanned int64     `json:"versionsScanned"`
	FactsDeleted    int64     `json:"factsDeleted"`
	FactsInserted   int64     `json:"factsInserted"`
	ChangesDeleted  int64     `json:"changesDeleted"`
	ChangesInserted int64     `json:"changesInserted"`
	EdgesDeleted    int64     `json:"edgesDeleted"`
	EdgesInserted   int64     `json:"edgesInserted"`
}

type reindexPlan struct {
	ObjectIDs []int64
	Items     []reindexItem
}

type reindexItem struct {
	Version  BackfillVersion
	Cluster  int64
	Kind     int64
	Object   int64
	Ref      versionRef
	Evidence extractor.Evidence
	Existing reindexCounts
}

type reindexCounts struct {
	Facts   int64
	Changes int64
	Edges   int64
}

type versionRef struct {
	Namespace string
	Name      string
}

func (s *Store) ReindexEvidence(ctx context.Context, opts ReindexOptions) (ReindexReport, error) {
	report := ReindexReport{StartedAt: time.Now().UTC(), DryRun: opts.DryRun}
	logger := logging.FromContext(ctx).With("component", "reindex")
	mode := "apply"
	if opts.DryRun {
		mode = "dry-run"
	}
	batchObjects := opts.BatchObjects
	if batchObjects <= 0 {
		batchObjects = defaultBackfillBatchObjects
	}
	logger.Info("starting evidence reindex", "mode", mode, "batchObjects", batchObjects)

	cursorObjectID := int64(0)
	for {
		objectIDs, err := s.nextBackfillObjectIDs(ctx, cursorObjectID, batchObjects)
		if err != nil {
			return ReindexReport{}, err
		}
		if len(objectIDs) == 0 {
			break
		}
		cursorObjectID = objectIDs[len(objectIDs)-1]
		plan, err := s.planEvidenceReindex(ctx, objectIDs, opts.Evidence, &report, logger)
		if err != nil {
			return ReindexReport{}, err
		}
		if len(plan.ObjectIDs) == 0 {
			continue
		}
		report.Batches++
		logger.Info(
			"planned evidence reindex batch",
			"batch", report.Batches,
			"objects", len(plan.ObjectIDs),
			"versions", len(plan.Items),
			"factsInserted", report.FactsInserted,
			"changesInserted", report.ChangesInserted,
			"edgesInserted", report.EdgesInserted,
		)
		if opts.DryRun {
			continue
		}
		if err := s.applyEvidenceReindexBatch(ctx, report.Batches, plan, &report, logger); err != nil {
			return ReindexReport{}, err
		}
	}

	report.FinishedAt = time.Now().UTC()
	logger.Info(
		"evidence reindex completed",
		"mode", mode,
		"batches", report.Batches,
		"objects", report.Objects,
		"versionsScanned", report.VersionsScanned,
		"factsDeleted", report.FactsDeleted,
		"factsInserted", report.FactsInserted,
		"changesDeleted", report.ChangesDeleted,
		"changesInserted", report.ChangesInserted,
		"edgesDeleted", report.EdgesDeleted,
		"edgesInserted", report.EdgesInserted,
	)
	return report, nil
}

func (s *Store) planEvidenceReindex(ctx context.Context, objectIDs []int64, evidence EvidenceFunc, report *ReindexReport, logger *slog.Logger) (reindexPlan, error) {
	plan := reindexPlan{
		ObjectIDs: objectIDs,
		Items:     make([]reindexItem, 0),
	}
	rows, err := s.queryBackfillVersions(ctx, objectIDs)
	if err != nil {
		return reindexPlan{}, err
	}
	defer rows.Close()

	seenObjects := make(map[int64]bool, len(objectIDs))
	for rows.Next() {
		item, err := scanReindexItem(rows)
		if err != nil {
			return reindexPlan{}, err
		}
		report.VersionsScanned++
		if !seenObjects[item.Object] {
			seenObjects[item.Object] = true
			report.Objects++
		}
		if evidence != nil {
			item.Evidence, err = evidence(ctx, item.Version)
			if err != nil {
				return reindexPlan{}, err
			}
		}
		report.FactsDeleted += item.Existing.Facts
		report.ChangesDeleted += item.Existing.Changes
		report.EdgesDeleted += item.Existing.Edges
		report.FactsInserted += int64(len(item.Evidence.Facts))
		report.ChangesInserted += int64(len(item.Evidence.Changes))
		report.EdgesInserted += int64(len(item.Evidence.Edges))
		plan.Items = append(plan.Items, item)
		if report.VersionsScanned%backfillProgressInterval == 0 {
			logger.Info("evidence reindex scan progress", "objects", report.Objects, "versionsScanned", report.VersionsScanned)
		}
	}
	return plan, rows.Err()
}

func (s *Store) queryBackfillVersions(ctx context.Context, objectIDs []int64) (*sql.Rows, error) {
	placeholderList := placeholders(len(objectIDs))
	args := make([]any, 0, len(objectIDs))
	for _, objectID := range objectIDs {
		args = append(args, objectID)
	}
	return s.db.QueryContext(ctx, `
select
  v.id,
  v.object_id,
  v.seq,
  v.observed_at,
  coalesce((
    select oo.observation_type
    from object_observations oo
    where oo.version_id = v.id
    order by oo.observed_at
    limit 1
  ), 'MODIFIED'),
  coalesce(v.resource_version, ''),
  c.id,
  c.name,
  ok.id,
  ar.api_group,
  ar.api_version,
  ar.resource,
  ok.kind,
  coalesce(o.namespace, ''),
  o.name,
  coalesce(o.uid, ''),
  b.data,
  (select count(*) from object_facts of where of.version_id = v.id),
  (select count(*) from object_changes oc where oc.version_id = v.id),
  (select count(*) from object_edges oe where oe.src_version_id = v.id or oe.dst_version_id = v.id)
from versions v
join objects o on o.id = v.object_id
join clusters c on c.id = o.cluster_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
join blobs b on b.digest = v.blob_ref
where b.codec = 'identity'
  and v.object_id in (`+placeholderList+`)
order by v.object_id, v.seq`, args...)
}

func scanReindexItem(rows *sql.Rows) (reindexItem, error) {
	var item reindexItem
	var observedAtMillis int64
	var observationType string
	if err := rows.Scan(
		&item.Version.VersionID,
		&item.Version.ObjectID,
		&item.Version.Sequence,
		&observedAtMillis,
		&observationType,
		&item.Version.ResourceVersion,
		&item.Cluster,
		&item.Version.Ref.ClusterID,
		&item.Kind,
		&item.Version.Ref.Group,
		&item.Version.Ref.Version,
		&item.Version.Ref.Resource,
		&item.Version.Ref.Kind,
		&item.Version.Ref.Namespace,
		&item.Version.Ref.Name,
		&item.Version.Ref.UID,
		&item.Version.Document,
		&item.Existing.Facts,
		&item.Existing.Changes,
		&item.Existing.Edges,
	); err != nil {
		return reindexItem{}, err
	}
	item.Object = item.Version.ObjectID
	item.Version.ObservedAt = time.UnixMilli(observedAtMillis)
	item.Version.ObservationType = observationTypeFromStored(observationType)
	item.Ref = versionRef{Namespace: item.Version.Ref.Namespace, Name: item.Version.Ref.Name}
	return item, nil
}

func observationTypeFromStored(value string) core.ObservationType {
	switch value {
	case string(core.ObservationAdded):
		return core.ObservationAdded
	case string(core.ObservationModified):
		return core.ObservationModified
	case string(core.ObservationDeleted):
		return core.ObservationDeleted
	case string(core.ObservationBookmark):
		return core.ObservationBookmark
	default:
		return core.ObservationModified
	}
}

func (s *Store) applyEvidenceReindexBatch(ctx context.Context, batchIndex int64, plan reindexPlan, report *ReindexReport, logger *slog.Logger) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	logger.Info("applying evidence reindex batch", "batch", batchIndex, "versions", len(plan.Items))
	for index, item := range plan.Items {
		if err := deleteVersionEvidence(ctx, tx, item.Version.VersionID); err != nil {
			return err
		}
		if err := putFacts(ctx, tx, item.Cluster, item.Kind, item.Object, item.Version.VersionID, item.Version.Ref, item.Evidence.Facts); err != nil {
			return err
		}
		if err := putChanges(ctx, tx, item.Cluster, item.Object, item.Version.VersionID, item.Evidence.Changes); err != nil {
			return err
		}
		obs := versionObservation(item.Version)
		if err := s.putEdges(ctx, tx, item.Cluster, item.Object, item.Version.VersionID, obs, item.Evidence.Edges); err != nil {
			return err
		}
		done := int64(index + 1)
		if done%backfillApplyInterval == 0 || index == len(plan.Items)-1 {
			logger.Info("evidence reindex apply progress", "batch", batchIndex, "versions", done, "total", len(plan.Items), "percent", progressPercent(done, int64(len(plan.Items))))
		}
	}
	logger.Info("committing evidence reindex batch", "batch", batchIndex)
	return tx.Commit()
}

func deleteVersionEvidence(ctx context.Context, tx *sql.Tx, versionID int64) error {
	for _, stmt := range []string{
		`delete from object_facts where version_id = ?`,
		`delete from object_changes where version_id = ?`,
		`delete from object_edges where src_version_id = ? or dst_version_id = ?`,
	} {
		if stmt == `delete from object_edges where src_version_id = ? or dst_version_id = ?` {
			if _, err := tx.ExecContext(ctx, stmt, versionID, versionID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt, versionID); err != nil {
			return err
		}
	}
	return nil
}

func versionObservation(version BackfillVersion) core.Observation {
	var object map[string]any
	_ = json.Unmarshal(version.Document, &object)
	return core.Observation{
		Type:            version.ObservationType,
		ObservedAt:      version.ObservedAt,
		ResourceVersion: version.ResourceVersion,
		Ref:             version.Ref,
		Object:          object,
	}
}
