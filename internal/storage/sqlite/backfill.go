package sqlite

import (
	"context"
	"database/sql"
	"time"

	"kube-insight/internal/core"
)

type BackfillNormalizeFunc func(context.Context, BackfillVersion) ([]byte, error)

type BackfillOptions struct {
	DryRun    bool
	Normalize BackfillNormalizeFunc
}

type BackfillVersion struct {
	VersionID       int64
	ObjectID        int64
	Sequence        int64
	ObservedAt      time.Time
	ResourceVersion string
	Ref             core.ResourceRef
	Document        []byte
}

type BackfillReport struct {
	StartedAt              time.Time `json:"startedAt"`
	FinishedAt             time.Time `json:"finishedAt"`
	DryRun                 bool      `json:"dryRun"`
	Objects                int64     `json:"objects"`
	VersionsScanned        int64     `json:"versionsScanned"`
	VersionsUpdated        int64     `json:"versionsUpdated"`
	VersionsPruned         int64     `json:"versionsPruned"`
	ObservationsRepointed  int64     `json:"observationsRepointed"`
	FactsDeleted           int64     `json:"factsDeleted"`
	ChangesDeleted         int64     `json:"changesDeleted"`
	EdgesDeleted           int64     `json:"edgesDeleted"`
	BlobsDeleted           int64     `json:"blobsDeleted"`
	BytesBefore            int64     `json:"bytesBefore"`
	BytesAfter             int64     `json:"bytesAfter,omitempty"`
	BytesReclaimedEstimate int64     `json:"bytesReclaimedEstimate"`
}

type backfillUpdate struct {
	VersionID int64
	OldHash   string
	NewHash   string
	Data      []byte
}

type backfillPrune struct {
	VersionID   int64
	CanonicalID int64
	OldHash     string
}

func (s *Store) BackfillRetainedHistory(ctx context.Context, opts BackfillOptions) (BackfillReport, error) {
	report := BackfillReport{StartedAt: time.Now().UTC(), DryRun: opts.DryRun}
	before, err := databaseFilesBytes(s.path)
	if err != nil {
		return BackfillReport{}, err
	}
	report.BytesBefore = before
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BackfillReport{}, err
	}
	defer rollback(tx)

	rows, err := tx.QueryContext(ctx, `
select
  v.id,
  v.object_id,
  v.seq,
  v.observed_at,
  coalesce(v.resource_version, ''),
  v.doc_hash,
  v.stored_size,
  c.name,
  ar.api_group,
  ar.api_version,
  ar.resource,
  ok.kind,
  coalesce(o.namespace, ''),
  o.name,
  coalesce(o.uid, ''),
  b.data
from versions v
join objects o on o.id = v.object_id
join clusters c on c.id = o.cluster_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
join blobs b on b.digest = v.blob_ref
where b.codec = 'identity'
order by v.object_id, v.seq`)
	if err != nil {
		return BackfillReport{}, err
	}
	defer rows.Close()

	blobRefs := map[string]int64{}
	blobBytes := map[string]int64{}
	lastObjectID := int64(0)
	lastDigest := ""
	lastKeptID := int64(0)
	updates := make([]backfillUpdate, 0)
	prunes := make([]backfillPrune, 0)
	seenObjects := map[int64]bool{}

	for rows.Next() {
		var version BackfillVersion
		var observedAtMillis int64
		var oldHash string
		var oldStoredSize int64
		if err := rows.Scan(
			&version.VersionID,
			&version.ObjectID,
			&version.Sequence,
			&observedAtMillis,
			&version.ResourceVersion,
			&oldHash,
			&oldStoredSize,
			&version.Ref.ClusterID,
			&version.Ref.Group,
			&version.Ref.Version,
			&version.Ref.Resource,
			&version.Ref.Kind,
			&version.Ref.Namespace,
			&version.Ref.Name,
			&version.Ref.UID,
			&version.Document,
		); err != nil {
			return BackfillReport{}, err
		}
		version.ObservedAt = time.UnixMilli(observedAtMillis)
		report.VersionsScanned++
		if !seenObjects[version.ObjectID] {
			seenObjects[version.ObjectID] = true
			report.Objects++
		}
		blobRefs[oldHash]++
		blobBytes[oldHash] = oldStoredSize

		normalized := version.Document
		if opts.Normalize != nil {
			normalized, err = opts.Normalize(ctx, version)
			if err != nil {
				return BackfillReport{}, err
			}
		}
		newHash := digest(normalized)
		if version.ObjectID != lastObjectID {
			lastObjectID = version.ObjectID
			lastDigest = ""
			lastKeptID = 0
		}
		if lastKeptID != 0 && newHash == lastDigest {
			prunes = append(prunes, backfillPrune{VersionID: version.VersionID, CanonicalID: lastKeptID, OldHash: oldHash})
			continue
		}
		lastDigest = newHash
		lastKeptID = version.VersionID
		if newHash != oldHash {
			updates = append(updates, backfillUpdate{VersionID: version.VersionID, OldHash: oldHash, NewHash: newHash, Data: normalized})
		}
	}
	if err := rows.Err(); err != nil {
		return BackfillReport{}, err
	}

	report.VersionsUpdated = int64(len(updates))
	report.VersionsPruned = int64(len(prunes))
	report.BytesReclaimedEstimate = estimateBackfillBlobReclaim(blobRefs, blobBytes, updates, prunes)
	if err := stageBackfillPrunes(ctx, tx, prunes); err != nil {
		return BackfillReport{}, err
	}
	if err := countBackfillPruneEffects(ctx, tx, &report); err != nil {
		return BackfillReport{}, err
	}
	if opts.DryRun {
		report.FinishedAt = time.Now().UTC()
		report.BytesAfter = report.BytesBefore
		return report, tx.Rollback()
	}
	if err := applyBackfillUpdates(ctx, tx, updates); err != nil {
		return BackfillReport{}, err
	}
	if err := applyBackfillPrunes(ctx, tx, prunes, &report); err != nil {
		return BackfillReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return BackfillReport{}, err
	}
	after, err := databaseFilesBytes(s.path)
	if err != nil {
		return BackfillReport{}, err
	}
	report.BytesAfter = after
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

func estimateBackfillBlobReclaim(blobRefs map[string]int64, blobBytes map[string]int64, updates []backfillUpdate, prunes []backfillPrune) int64 {
	refs := make(map[string]int64, len(blobRefs))
	for digest, count := range blobRefs {
		refs[digest] = count
	}
	for _, update := range updates {
		refs[update.OldHash]--
		refs[update.NewHash]++
	}
	for _, prune := range prunes {
		refs[prune.OldHash]--
	}
	var bytes int64
	for digest, count := range refs {
		if count <= 0 {
			bytes += blobBytes[digest]
		}
	}
	return bytes
}

func applyBackfillUpdates(ctx context.Context, tx *sql.Tx, updates []backfillUpdate) error {
	for _, update := range updates {
		if err := putBlob(ctx, tx, update.NewHash, update.Data); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update versions
set doc_hash = ?, blob_ref = ?, raw_size = ?, stored_size = ?
where id = ?`, update.NewHash, update.NewHash, len(update.Data), len(update.Data), update.VersionID); err != nil {
			return err
		}
	}
	return nil
}

func applyBackfillPrunes(ctx context.Context, tx *sql.Tx, prunes []backfillPrune, report *BackfillReport) error {
	if len(prunes) == 0 {
		var err error
		report.BlobsDeleted, err = deleteUnreferencedBlobs(ctx, tx)
		return err
	}
	if err := stageBackfillPrunes(ctx, tx, prunes); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
update object_observations
set version_id = (select canonical_id from backfill_prune where id = object_observations.version_id),
    content_changed = 0
where version_id in (select id from backfill_prune)`)
	if err != nil {
		return err
	}
	report.ObservationsRepointed, _ = res.RowsAffected()
	for _, stmt := range []string{
		`update latest_index
set latest_version_id = (select canonical_id from backfill_prune where id = latest_index.latest_version_id)
where latest_version_id in (select id from backfill_prune)`,
		`update objects
set latest_version_id = (select li.latest_version_id from latest_index li where li.object_id = objects.id)
where latest_version_id in (select id from backfill_prune)`,
		`update versions set parent_version_id = null where parent_version_id in (select id from backfill_prune)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if res, err = tx.ExecContext(ctx, `delete from object_facts where version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	report.FactsDeleted, _ = res.RowsAffected()
	if res, err = tx.ExecContext(ctx, `delete from object_changes where version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	report.ChangesDeleted, _ = res.RowsAffected()
	if res, err = tx.ExecContext(ctx, `
delete from object_edges
where src_version_id in (select id from backfill_prune)
   or dst_version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	report.EdgesDeleted, _ = res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `delete from versions where id in (select id from backfill_prune)`); err != nil {
		return err
	}
	report.BlobsDeleted, err = deleteUnreferencedBlobs(ctx, tx)
	return err
}

func stageBackfillPrunes(ctx context.Context, tx *sql.Tx, prunes []backfillPrune) error {
	if _, err := tx.ExecContext(ctx, `create temp table if not exists backfill_prune(id integer primary key, canonical_id integer not null)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from backfill_prune`); err != nil {
		return err
	}
	for _, prune := range prunes {
		if _, err := tx.ExecContext(ctx, `insert into backfill_prune(id, canonical_id) values(?, ?)`, prune.VersionID, prune.CanonicalID); err != nil {
			return err
		}
	}
	return nil
}

func countBackfillPruneEffects(ctx context.Context, tx *sql.Tx, report *BackfillReport) error {
	for _, item := range []struct {
		target *int64
		query  string
	}{
		{&report.ObservationsRepointed, `select count(*) from object_observations where version_id in (select id from backfill_prune)`},
		{&report.FactsDeleted, `select count(*) from object_facts where version_id in (select id from backfill_prune)`},
		{&report.ChangesDeleted, `select count(*) from object_changes where version_id in (select id from backfill_prune)`},
		{&report.EdgesDeleted, `select count(*) from object_edges where src_version_id in (select id from backfill_prune) or dst_version_id in (select id from backfill_prune)`},
	} {
		if err := tx.QueryRowContext(ctx, item.query).Scan(item.target); err != nil {
			return err
		}
	}
	return nil
}

func deleteUnreferencedBlobs(ctx context.Context, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx, `delete from blobs where digest not in (select distinct blob_ref from versions)`)
	if err != nil {
		return 0, err
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}
