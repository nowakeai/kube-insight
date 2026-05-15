package sqlite

import (
	"context"
	"os"
	"time"

	"kube-insight/internal/logging"
)

type CompactReport struct {
	StartedAt            time.Time                   `json:"startedAt"`
	FinishedAt           time.Time                   `json:"finishedAt"`
	BytesBefore          int64                       `json:"bytesBefore"`
	BytesAfter           int64                       `json:"bytesAfter"`
	BytesReclaimed       int64                       `json:"bytesReclaimed"`
	Pruned               *PruneReport                `json:"pruned,omitempty"`
	FilterRollup         *FilterDecisionRollupReport `json:"filterRollup,omitempty"`
	DeletedRawLatestRows int64                       `json:"deletedRawLatestRows"`
	StatsBefore          Stats                       `json:"statsBefore"`
	StatsAfter           Stats                       `json:"statsAfter"`
}

type CompactOptions struct {
	PruneUnchanged bool
}

type DatabaseFileStats struct {
	DatabaseBytes int64 `json:"databaseBytes"`
	WALBytes      int64 `json:"walBytes"`
	TotalBytes    int64 `json:"totalBytes"`
}

func (s *Store) Compact(ctx context.Context, options ...CompactOptions) (CompactReport, error) {
	opts := CompactOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	logger := logging.FromContext(ctx).With("component", "compact")
	report := CompactReport{StartedAt: time.Now().UTC()}
	logger.Info("starting storage compaction", "pruneUnchanged", opts.PruneUnchanged)
	beforeBytes, err := databaseFilesBytes(s.path)
	if err != nil {
		return CompactReport{}, err
	}
	report.BytesBefore = beforeBytes
	logger.Info("measured database files before compaction", "bytesBefore", report.BytesBefore)
	logger.Info("collecting storage stats before compaction")
	statsBefore, err := s.Stats(ctx)
	if err != nil {
		return CompactReport{}, err
	}
	report.StatsBefore = statsBefore
	logger.Info(
		"collected storage stats before compaction",
		"versions", report.StatsBefore.Versions,
		"blobs", report.StatsBefore.Blobs,
		"filterDecisions", report.StatsBefore.FilterDecisions,
		"filterDecisionRollups", report.StatsBefore.FilterDecisionRollups,
	)
	if opts.PruneUnchanged {
		logger.Info("pruning unchanged versions before compaction")
		pruned, err := s.PruneUnchangedVersions(ctx)
		if err != nil {
			return CompactReport{}, err
		}
		report.Pruned = &pruned
		logger.Info("pruned unchanged versions", "versions", pruned.Versions, "facts", pruned.Facts, "changes", pruned.Changes, "edges", pruned.Edges, "blobs", pruned.Blobs)
	}
	logger.Info("rolling up routine filter decisions")
	filterRollup, err := s.RollupRoutineFilterDecisions(ctx)
	if err != nil {
		return CompactReport{}, err
	}
	report.FilterRollup = &filterRollup
	logger.Info(
		"rolled up routine filter decisions",
		"rowsRolledUp", filterRollup.RowsRolledUp,
		"rollupsAffected", filterRollup.RollupsAffected,
		"duration", filterRollup.FinishedAt.Sub(filterRollup.StartedAt).String(),
	)
	logger.Info("pruning raw latest rows for deleted objects")
	deletedRawLatestRows, err := s.DeleteRawLatestForDeletedObjects(ctx)
	if err != nil {
		return CompactReport{}, err
	}
	report.DeletedRawLatestRows = deletedRawLatestRows
	logger.Info("pruned raw latest rows for deleted objects", "rows", deletedRawLatestRows)

	for _, step := range []struct {
		name string
		stmt string
	}{
		{"optimize", `pragma optimize`},
		{"vacuum", `vacuum`},
		{"checkpoint", `pragma wal_checkpoint(truncate)`},
	} {
		startedAt := time.Now()
		logger.Info("running compact step", "step", step.name)
		if _, err := s.db.ExecContext(ctx, step.stmt); err != nil {
			return CompactReport{}, err
		}
		logger.Info("completed compact step", "step", step.name, "duration", time.Since(startedAt).String())
	}

	logger.Info("measuring database files after compaction")
	afterBytes, err := databaseFilesBytes(s.path)
	if err != nil {
		return CompactReport{}, err
	}
	logger.Info("collecting storage stats after compaction")
	statsAfter, err := s.Stats(ctx)
	if err != nil {
		return CompactReport{}, err
	}
	report.BytesAfter = afterBytes
	report.StatsAfter = statsAfter
	report.FinishedAt = time.Now().UTC()
	if beforeBytes > afterBytes {
		report.BytesReclaimed = beforeBytes - afterBytes
	}
	logger.Info(
		"storage compaction completed",
		"duration", report.FinishedAt.Sub(report.StartedAt).String(),
		"bytesBefore", report.BytesBefore,
		"bytesAfter", report.BytesAfter,
		"bytesReclaimed", report.BytesReclaimed,
	)
	return report, nil
}

func databaseFilesBytes(path string) (int64, error) {
	stats, err := databaseFileStats(path)
	if err != nil {
		return 0, err
	}
	return stats.TotalBytes, nil
}

func databaseFileStats(path string) (DatabaseFileStats, error) {
	dbBytes, err := fileSizeIfExists(path)
	if err != nil {
		return DatabaseFileStats{}, err
	}
	walBytes, err := fileSizeIfExists(path + "-wal")
	if err != nil {
		return DatabaseFileStats{}, err
	}
	return DatabaseFileStats{
		DatabaseBytes: dbBytes,
		WALBytes:      walBytes,
		TotalBytes:    dbBytes + walBytes,
	}, nil
}

func fileSizeIfExists(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}
