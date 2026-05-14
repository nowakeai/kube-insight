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
	var total int64
	for _, candidate := range []string{path, path + "-wal"} {
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}
