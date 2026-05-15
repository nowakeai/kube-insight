package sqlite

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/logging"
)

type BackfillNormalizeFunc func(context.Context, BackfillVersion) ([]byte, error)

const (
	defaultBackfillBatchObjects       = 500
	backfillProgressInterval    int64 = 10000
	backfillApplyInterval       int64 = 5000
	backfillDeleteChunkSize           = 500
)

type BackfillOptions struct {
	DryRun       bool
	Normalize    BackfillNormalizeFunc
	BatchObjects int
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
	Batches                int64     `json:"batches"`
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

type backfillBatch struct {
	ObjectIDs []int64
	BlobRefs  map[string]int64
	BlobBytes map[string]int64
	Updates   []backfillUpdate
	Prunes    []backfillPrune
}

func (s *Store) BackfillRetainedHistory(ctx context.Context, opts BackfillOptions) (BackfillReport, error) {
	report := BackfillReport{StartedAt: time.Now().UTC(), DryRun: opts.DryRun}
	logger := logging.FromContext(ctx).With("component", "backfill")
	mode := "apply"
	if opts.DryRun {
		mode = "dry-run"
	}
	batchObjects := opts.BatchObjects
	if batchObjects <= 0 {
		batchObjects = defaultBackfillBatchObjects
	}
	before, err := databaseFilesBytes(s.path)
	if err != nil {
		return BackfillReport{}, err
	}
	report.BytesBefore = before
	logger.Info("starting history backfill", "mode", mode, "batchObjects", batchObjects, "bytesBefore", report.BytesBefore)

	cursorObjectID := int64(0)
	lastProgressLogged := int64(0)
	for {
		objectIDs, err := s.nextBackfillObjectIDs(ctx, cursorObjectID, batchObjects)
		if err != nil {
			return BackfillReport{}, err
		}
		if len(objectIDs) == 0 {
			break
		}
		cursorObjectID = objectIDs[len(objectIDs)-1]
		batch, err := s.planBackfillBatch(ctx, objectIDs, opts.Normalize, &report, &lastProgressLogged, logger)
		if err != nil {
			return BackfillReport{}, err
		}
		if len(batch.ObjectIDs) == 0 {
			continue
		}
		report.Batches++
		report.VersionsUpdated += int64(len(batch.Updates))
		report.VersionsPruned += int64(len(batch.Prunes))
		report.BytesReclaimedEstimate += estimateBackfillBlobReclaim(batch.BlobRefs, batch.BlobBytes, batch.Updates, batch.Prunes)
		logger.Info(
			"planned backfill batch",
			"batch", report.Batches,
			"objects", len(batch.ObjectIDs),
			"versionsUpdated", len(batch.Updates),
			"versionsPruned", len(batch.Prunes),
		)
		if opts.DryRun {
			if err := s.countBackfillPruneEffectsForPrunes(ctx, batch.Prunes, &report, logger); err != nil {
				return BackfillReport{}, err
			}
			continue
		}
		if err := s.applyBackfillBatch(ctx, report.Batches, batch, &report, logger); err != nil {
			return BackfillReport{}, err
		}
	}

	logger.Info(
		"scanned retained history",
		"batches", report.Batches,
		"objects", report.Objects,
		"versionsScanned", report.VersionsScanned,
		"versionsUpdated", report.VersionsUpdated,
		"versionsPruned", report.VersionsPruned,
		"bytesReclaimedEstimate", report.BytesReclaimedEstimate,
	)
	after, err := databaseFilesBytes(s.path)
	if err != nil {
		return BackfillReport{}, err
	}
	report.BytesAfter = after
	if opts.DryRun {
		report.BytesAfter = report.BytesBefore
	}
	report.FinishedAt = time.Now().UTC()
	logger.Info(
		"history backfill completed",
		"mode", mode,
		"duration", report.FinishedAt.Sub(report.StartedAt).String(),
		"bytesBefore", report.BytesBefore,
		"bytesAfter", report.BytesAfter,
		"bytesReclaimedEstimate", report.BytesReclaimedEstimate,
		"blobsDeleted", report.BlobsDeleted,
	)
	return report, nil
}

func (s *Store) nextBackfillObjectIDs(ctx context.Context, afterObjectID int64, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
select distinct v.object_id
from versions v
join blobs b on b.digest = v.blob_ref
where b.codec = 'identity'
  and v.object_id > ?
order by v.object_id
limit ?`, afterObjectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objectIDs []int64
	for rows.Next() {
		var objectID int64
		if err := rows.Scan(&objectID); err != nil {
			return nil, err
		}
		objectIDs = append(objectIDs, objectID)
	}
	return objectIDs, rows.Err()
}

func (s *Store) planBackfillBatch(
	ctx context.Context,
	objectIDs []int64,
	normalize BackfillNormalizeFunc,
	report *BackfillReport,
	lastProgressLogged *int64,
	logger *slog.Logger,
) (backfillBatch, error) {
	batch := backfillBatch{
		ObjectIDs: objectIDs,
		BlobRefs:  map[string]int64{},
		BlobBytes: map[string]int64{},
		Updates:   make([]backfillUpdate, 0),
		Prunes:    make([]backfillPrune, 0),
	}
	placeholderList := placeholders(len(objectIDs))
	args := make([]any, 0, len(objectIDs))
	for _, objectID := range objectIDs {
		args = append(args, objectID)
	}
	rows, err := s.db.QueryContext(ctx, `
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
  and v.object_id in (`+placeholderList+`)
order by v.object_id, v.seq`, args...)
	if err != nil {
		return backfillBatch{}, err
	}
	defer rows.Close()

	lastObjectID := int64(0)
	lastDigest := ""
	lastKeptID := int64(0)
	seenObjects := make(map[int64]bool, len(objectIDs))
	logProgress := func() {
		if report.VersionsScanned == 0 || report.VersionsScanned%backfillProgressInterval != 0 || report.VersionsScanned == *lastProgressLogged {
			return
		}
		*lastProgressLogged = report.VersionsScanned
		logger.Info(
			"backfill scan progress",
			"objects", report.Objects,
			"versionsScanned", report.VersionsScanned,
			"versionsUpdated", report.VersionsUpdated+int64(len(batch.Updates)),
			"versionsPruned", report.VersionsPruned+int64(len(batch.Prunes)),
		)
	}

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
			return backfillBatch{}, err
		}
		version.ObservedAt = time.UnixMilli(observedAtMillis)
		report.VersionsScanned++
		if !seenObjects[version.ObjectID] {
			seenObjects[version.ObjectID] = true
			report.Objects++
		}
		batch.BlobRefs[oldHash]++
		batch.BlobBytes[oldHash] = oldStoredSize

		normalized := version.Document
		if normalize != nil {
			normalized, err = normalize(ctx, version)
			if err != nil {
				return backfillBatch{}, err
			}
		}
		newHash := digest(normalized)
		if version.ObjectID != lastObjectID {
			lastObjectID = version.ObjectID
			lastDigest = ""
			lastKeptID = 0
		}
		if lastKeptID != 0 && newHash == lastDigest {
			batch.Prunes = append(batch.Prunes, backfillPrune{VersionID: version.VersionID, CanonicalID: lastKeptID, OldHash: oldHash})
			logProgress()
			continue
		}
		lastDigest = newHash
		lastKeptID = version.VersionID
		if newHash != oldHash {
			batch.Updates = append(batch.Updates, backfillUpdate{VersionID: version.VersionID, OldHash: oldHash, NewHash: newHash, Data: normalized})
		}
		logProgress()
	}
	if err := rows.Err(); err != nil {
		return backfillBatch{}, err
	}
	return batch, nil
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

func (s *Store) countBackfillPruneEffectsForPrunes(ctx context.Context, prunes []backfillPrune, report *BackfillReport, logger *slog.Logger) error {
	if len(prunes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := stageBackfillPrunes(ctx, tx, prunes, logger); err != nil {
		return err
	}
	if err := countBackfillPruneEffects(ctx, tx, report); err != nil {
		return err
	}
	return tx.Rollback()
}

func (s *Store) applyBackfillBatch(ctx context.Context, batchIndex int64, batch backfillBatch, report *BackfillReport, logger *slog.Logger) error {
	logger.Info(
		"applying backfill batch",
		"batch", batchIndex,
		"objects", len(batch.ObjectIDs),
		"versionsUpdated", len(batch.Updates),
		"versionsPruned", len(batch.Prunes),
	)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := applyBackfillUpdates(ctx, tx, batch.Updates, logger); err != nil {
		return err
	}
	if err := applyBackfillPrunes(ctx, tx, batch.Prunes, report, logger); err != nil {
		return err
	}
	deleted, err := deleteBackfillCandidateBlobs(ctx, tx, backfillCandidateBlobDigests(batch.Updates, batch.Prunes), logger)
	if err != nil {
		return err
	}
	report.BlobsDeleted += deleted
	logger.Info("committing backfill batch", "batch", batchIndex)
	if err := tx.Commit(); err != nil {
		return err
	}
	logger.Info("committed backfill batch", "batch", batchIndex, "blobsDeleted", deleted)
	return nil
}

func applyBackfillUpdates(ctx context.Context, tx *sql.Tx, updates []backfillUpdate, logger *slog.Logger) error {
	if len(updates) == 0 {
		logger.Info("backfill version updates skipped")
		return nil
	}
	logger.Info("applying backfill version updates", "versionsUpdated", len(updates))
	for index, update := range updates {
		if err := putBlob(ctx, tx, update.NewHash, update.Data); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
update versions
set doc_hash = ?, blob_ref = ?, raw_size = ?, stored_size = ?
where id = ?`, update.NewHash, update.NewHash, len(update.Data), len(update.Data), update.VersionID); err != nil {
			return err
		}
		applied := int64(index + 1)
		if applied%backfillApplyInterval == 0 || index == len(updates)-1 {
			logger.Info("backfill version update progress", "applied", applied, "total", len(updates))
		}
	}
	return nil
}

func applyBackfillPrunes(ctx context.Context, tx *sql.Tx, prunes []backfillPrune, report *BackfillReport, logger *slog.Logger) error {
	if len(prunes) == 0 {
		logger.Info("backfill prune skipped")
		return nil
	}
	logger.Info("staging backfill prunes", "versionsPruned", len(prunes))
	if err := stageBackfillPrunes(ctx, tx, prunes, logger); err != nil {
		return err
	}
	logger.Info("repointing observations from pruned versions")
	res, err := tx.ExecContext(ctx, `
update object_observations
set version_id = (select canonical_id from backfill_prune where id = object_observations.version_id),
    content_changed = 0
where version_id in (select id from backfill_prune)`)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	report.ObservationsRepointed += rows
	logger.Info("repointed observations", "observationsRepointed", rows)
	for _, stmt := range []struct {
		name  string
		query string
	}{
		{"latest index", `update latest_index
set latest_version_id = (select canonical_id from backfill_prune where id = latest_index.latest_version_id)
where latest_version_id in (select id from backfill_prune)`},
		{"objects latest version", `update objects
set latest_version_id = (select li.latest_version_id from latest_index li where li.object_id = objects.id)
where latest_version_id in (select id from backfill_prune)`},
		{"version parent links", `update versions set parent_version_id = null where parent_version_id in (select id from backfill_prune)`},
	} {
		logger.Info("updating backfill references", "target", stmt.name)
		if _, err := tx.ExecContext(ctx, stmt.query); err != nil {
			return err
		}
	}
	logger.Info("deleting facts for pruned versions")
	if res, err = tx.ExecContext(ctx, `delete from object_facts where version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	rows, _ = res.RowsAffected()
	report.FactsDeleted += rows
	logger.Info("deleted facts for pruned versions", "factsDeleted", rows)
	logger.Info("deleting changes for pruned versions")
	if res, err = tx.ExecContext(ctx, `delete from object_changes where version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	rows, _ = res.RowsAffected()
	report.ChangesDeleted += rows
	logger.Info("deleted changes for pruned versions", "changesDeleted", rows)
	logger.Info("deleting edges for pruned versions")
	if res, err = tx.ExecContext(ctx, `
delete from object_edges
where src_version_id in (select id from backfill_prune)
   or dst_version_id in (select id from backfill_prune)`); err != nil {
		return err
	}
	rows, _ = res.RowsAffected()
	report.EdgesDeleted += rows
	logger.Info("deleted edges for pruned versions", "edgesDeleted", rows)
	logger.Info("deleting pruned versions")
	if err := deleteBackfillPrunedVersions(ctx, tx, logger); err != nil {
		return err
	}
	return nil
}

func deleteBackfillPrunedVersions(ctx context.Context, tx *sql.Tx, logger *slog.Logger) error {
	var total int64
	if err := tx.QueryRowContext(ctx, `select count(*) from versions where id in (select id from backfill_prune)`).Scan(&total); err != nil {
		return err
	}
	deleted := int64(0)
	for {
		ids, err := selectBackfillPrunedVersionIDs(ctx, tx, backfillDeleteChunkSize)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		res, err := tx.ExecContext(ctx, `delete from versions where id in (`+placeholders(len(ids))+`)`, int64Args(ids)...)
		if err != nil {
			return err
		}
		rows, _ := res.RowsAffected()
		deleted += rows
		logger.Info("backfill pruned version delete progress", "deleted", deleted, "total", total, "percent", progressPercent(deleted, total))
	}
	return nil
}

func selectBackfillPrunedVersionIDs(ctx context.Context, tx *sql.Tx, limit int) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
select bp.id
from backfill_prune bp
join versions v on v.id = bp.id
order by bp.id
limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func stageBackfillPrunes(ctx context.Context, tx *sql.Tx, prunes []backfillPrune, logger *slog.Logger) error {
	if _, err := tx.ExecContext(ctx, `create temp table if not exists backfill_prune(id integer primary key, canonical_id integer not null)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from backfill_prune`); err != nil {
		return err
	}
	for index, prune := range prunes {
		if _, err := tx.ExecContext(ctx, `insert into backfill_prune(id, canonical_id) values(?, ?)`, prune.VersionID, prune.CanonicalID); err != nil {
			return err
		}
		staged := int64(index + 1)
		if staged%backfillApplyInterval == 0 || index == len(prunes)-1 {
			logger.Info("backfill prune staging progress", "staged", staged, "total", len(prunes))
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
		var count int64
		if err := tx.QueryRowContext(ctx, item.query).Scan(&count); err != nil {
			return err
		}
		*item.target += count
	}
	return nil
}

func backfillCandidateBlobDigests(updates []backfillUpdate, prunes []backfillPrune) []string {
	seen := map[string]bool{}
	var out []string
	for _, update := range updates {
		if update.OldHash == "" || seen[update.OldHash] {
			continue
		}
		seen[update.OldHash] = true
		out = append(out, update.OldHash)
	}
	for _, prune := range prunes {
		if prune.OldHash == "" || seen[prune.OldHash] {
			continue
		}
		seen[prune.OldHash] = true
		out = append(out, prune.OldHash)
	}
	return out
}

func int64Args(values []int64) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func stringArgs(values []string) []any {
	args := make([]any, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func deleteBackfillCandidateBlobs(ctx context.Context, tx *sql.Tx, digests []string, logger *slog.Logger) (int64, error) {
	if len(digests) == 0 {
		return 0, nil
	}
	logger.Info("staging backfill blob cleanup candidates", "candidates", len(digests))
	if _, err := tx.ExecContext(ctx, `create temp table if not exists backfill_blob_candidate(digest text primary key)`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `delete from backfill_blob_candidate`); err != nil {
		return 0, err
	}
	for index, digest := range digests {
		if _, err := tx.ExecContext(ctx, `insert or ignore into backfill_blob_candidate(digest) values(?)`, digest); err != nil {
			return 0, err
		}
		staged := int64(index + 1)
		if staged%backfillDeleteChunkSize == 0 || index == len(digests)-1 {
			logger.Info("backfill blob cleanup staging progress", "staged", staged, "total", len(digests), "percent", progressPercent(staged, int64(len(digests))))
		}
	}
	logger.Info("counting deletable backfill blob candidates")
	var total int64
	if err := tx.QueryRowContext(ctx, `
select count(*)
from backfill_blob_candidate bc
where exists (select 1 from blobs b where b.digest = bc.digest)
  and not exists (select 1 from versions v where v.blob_ref = bc.digest)`).Scan(&total); err != nil {
		return 0, err
	}
	logger.Info("counted deletable backfill blob candidates", "total", total)
	deleted := int64(0)
	for {
		chunk, err := selectBackfillBlobCleanupDigests(ctx, tx, backfillDeleteChunkSize)
		if err != nil {
			return 0, err
		}
		if len(chunk) == 0 {
			break
		}
		res, err := tx.ExecContext(ctx, `delete from blobs where digest in (`+placeholders(len(chunk))+`)`, stringArgs(chunk)...)
		if err != nil {
			return 0, err
		}
		rows, _ := res.RowsAffected()
		deleted += rows
		logger.Info("backfill blob cleanup progress", "deleted", deleted, "total", total, "percent", progressPercent(deleted, total))
	}
	logger.Info("deleted backfill blob cleanup candidates", "blobsDeleted", deleted)
	return deleted, nil
}

func selectBackfillBlobCleanupDigests(ctx context.Context, tx *sql.Tx, limit int) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
select bc.digest
from backfill_blob_candidate bc
where exists (select 1 from blobs b where b.digest = bc.digest)
  and not exists (select 1 from versions v where v.blob_ref = bc.digest)
order by bc.digest
limit ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var digests []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, err
		}
		digests = append(digests, digest)
	}
	return digests, rows.Err()
}

func progressPercent(done int64, total int64) string {
	if total <= 0 {
		return "100.0%"
	}
	percent := float64(done) / float64(total) * 100
	if percent > 100 {
		percent = 100
	}
	return strconv.FormatFloat(percent, 'f', 1, 64) + "%"
}
