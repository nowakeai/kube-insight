package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMaintenanceCheckpointsWAL(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.db.ExecContext(context.Background(), `
insert into maintenance_runs(backend, task, started_at, status)
values ('sqlite', 'test', 1, 'ok')`); err != nil {
		t.Fatal(err)
	}

	report, err := store.Maintenance(context.Background(), MaintenanceOptions{
		MinWalBytes:            0,
		IncrementalVacuumPages: 16,
		JournalSizeLimitBytes:  1024 * 1024,
		SkipWhenDatabaseBusy:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.StartedAt.IsZero() || report.FinishedAt.IsZero() {
		t.Fatalf("report timestamps missing: %#v", report)
	}
	if report.BytesAfter > report.BytesBefore {
		t.Fatalf("maintenance grew storage: before=%d after=%d", report.BytesBefore, report.BytesAfter)
	}
}

func TestMaintenanceSkipsBelowThreshold(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	report, err := store.Maintenance(context.Background(), MaintenanceOptions{
		MinWalBytes: 1 << 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Skipped || report.Reason != "below_threshold" {
		t.Fatalf("report = %#v", report)
	}
}
