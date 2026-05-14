package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type MaintenanceOptions struct {
	MinWalBytes                 int64
	IncrementalVacuumPages      int
	JournalSizeLimitBytes       int64
	SkipWhenDatabaseBusy        bool
	LogUnchangedMaintenanceRuns bool
}

type MaintenanceReport struct {
	StartedAt             time.Time `json:"startedAt"`
	FinishedAt            time.Time `json:"finishedAt"`
	BytesBefore           int64     `json:"bytesBefore"`
	BytesAfter            int64     `json:"bytesAfter"`
	BytesReclaimed        int64     `json:"bytesReclaimed"`
	DatabaseBytesBefore   int64     `json:"databaseBytesBefore"`
	DatabaseBytesAfter    int64     `json:"databaseBytesAfter"`
	WALBytesBefore        int64     `json:"walBytesBefore"`
	WALBytesAfter         int64     `json:"walBytesAfter"`
	PageCountBefore       int64     `json:"pageCountBefore"`
	PageCountAfter        int64     `json:"pageCountAfter"`
	FreelistPagesBefore   int64     `json:"freelistPagesBefore"`
	FreelistPagesAfter    int64     `json:"freelistPagesAfter"`
	WALCheckpointBusy     int64     `json:"walCheckpointBusy"`
	WALCheckpointLog      int64     `json:"walCheckpointLog"`
	WALCheckpointedFrames int64     `json:"walCheckpointedFrames"`
	Skipped               bool      `json:"skipped"`
	Reason                string    `json:"reason,omitempty"`
}

func (s *Store) Maintenance(ctx context.Context, options ...MaintenanceOptions) (MaintenanceReport, error) {
	opts := MaintenanceOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	report := MaintenanceReport{StartedAt: time.Now().UTC()}
	before, err := databaseFileStats(s.path)
	if err != nil {
		return MaintenanceReport{}, err
	}
	report.BytesBefore = before.TotalBytes
	report.DatabaseBytesBefore = before.DatabaseBytes
	report.WALBytesBefore = before.WALBytes
	report.PageCountBefore, _ = s.pragmaInt64(ctx, "page_count")
	report.FreelistPagesBefore, _ = s.pragmaInt64(ctx, "freelist_count")

	if opts.MinWalBytes > 0 && before.WALBytes < opts.MinWalBytes && report.FreelistPagesBefore == 0 {
		report.Skipped = true
		report.Reason = "below_threshold"
		report.FinishedAt = time.Now().UTC()
		report.BytesAfter = report.BytesBefore
		report.DatabaseBytesAfter = report.DatabaseBytesBefore
		report.WALBytesAfter = report.WALBytesBefore
		report.PageCountAfter = report.PageCountBefore
		report.FreelistPagesAfter = report.FreelistPagesBefore
		return report, nil
	}

	if opts.JournalSizeLimitBytes > 0 {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("pragma journal_size_limit = %d", opts.JournalSizeLimitBytes)); err != nil {
			return MaintenanceReport{}, err
		}
	}
	if _, err := s.db.ExecContext(ctx, `pragma optimize`); err != nil && !ignoreBusyMaintenanceError(err, opts) {
		return MaintenanceReport{}, err
	}
	busy, logFrames, checkpointed, err := s.walCheckpoint(ctx, "truncate")
	if err != nil {
		if !ignoreBusyMaintenanceError(err, opts) {
			return MaintenanceReport{}, err
		}
		report.Skipped = true
		report.Reason = "busy"
	} else {
		report.WALCheckpointBusy = busy
		report.WALCheckpointLog = logFrames
		report.WALCheckpointedFrames = checkpointed
		if busy > 0 && opts.SkipWhenDatabaseBusy {
			report.Skipped = true
			report.Reason = "busy"
		}
	}
	if opts.IncrementalVacuumPages > 0 && report.FreelistPagesBefore > 0 && !report.Skipped {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("pragma incremental_vacuum(%d)", opts.IncrementalVacuumPages)); err != nil && !ignoreBusyMaintenanceError(err, opts) {
			return MaintenanceReport{}, err
		}
	}

	after, err := databaseFileStats(s.path)
	if err != nil {
		return MaintenanceReport{}, err
	}
	report.BytesAfter = after.TotalBytes
	report.DatabaseBytesAfter = after.DatabaseBytes
	report.WALBytesAfter = after.WALBytes
	report.PageCountAfter, _ = s.pragmaInt64(ctx, "page_count")
	report.FreelistPagesAfter, _ = s.pragmaInt64(ctx, "freelist_count")
	report.FinishedAt = time.Now().UTC()
	if report.BytesBefore > report.BytesAfter {
		report.BytesReclaimed = report.BytesBefore - report.BytesAfter
	}
	return report, nil
}

func (s *Store) walCheckpoint(ctx context.Context, mode string) (int64, int64, int64, error) {
	if mode == "" {
		mode = "passive"
	}
	var busy, logFrames, checkpointed sql.NullInt64
	err := s.db.QueryRowContext(ctx, "pragma wal_checkpoint("+mode+")").Scan(&busy, &logFrames, &checkpointed)
	return busy.Int64, logFrames.Int64, checkpointed.Int64, err
}

func (s *Store) pragmaInt64(ctx context.Context, name string) (int64, error) {
	var value int64
	if err := s.db.QueryRowContext(ctx, "pragma "+name).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

func ignoreBusyMaintenanceError(err error, opts MaintenanceOptions) bool {
	if err == nil || !opts.SkipWhenDatabaseBusy {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "busy") || strings.Contains(text, "locked")
}
