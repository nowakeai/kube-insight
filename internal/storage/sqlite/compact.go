package sqlite

import (
	"context"
	"os"
	"time"
)

type CompactReport struct {
	StartedAt      time.Time    `json:"startedAt"`
	FinishedAt     time.Time    `json:"finishedAt"`
	BytesBefore    int64        `json:"bytesBefore"`
	BytesAfter     int64        `json:"bytesAfter"`
	BytesReclaimed int64        `json:"bytesReclaimed"`
	Pruned         *PruneReport `json:"pruned,omitempty"`
	StatsBefore    Stats        `json:"statsBefore"`
	StatsAfter     Stats        `json:"statsAfter"`
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
	report := CompactReport{StartedAt: time.Now().UTC()}
	beforeBytes, err := databaseFilesBytes(s.path)
	if err != nil {
		return CompactReport{}, err
	}
	report.BytesBefore = beforeBytes
	statsBefore, err := s.Stats(ctx)
	if err != nil {
		return CompactReport{}, err
	}
	report.StatsBefore = statsBefore
	if opts.PruneUnchanged {
		pruned, err := s.PruneUnchangedVersions(ctx)
		if err != nil {
			return CompactReport{}, err
		}
		report.Pruned = &pruned
	}

	for _, stmt := range []string{
		`pragma optimize`,
		`vacuum`,
		`pragma wal_checkpoint(truncate)`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return CompactReport{}, err
		}
	}

	afterBytes, err := databaseFilesBytes(s.path)
	if err != nil {
		return CompactReport{}, err
	}
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
