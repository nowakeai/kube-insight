package cli

import (
	"context"
	"sync"
	"time"

	"kube-insight/internal/collector"
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/storage/sqlite"
)

func startSQLiteMaintenanceLoop(ctx context.Context, logf collector.WatchLogFunc, store *sqlite.Store, cfg appconfig.MaintenanceConfig) func() {
	if store == nil || !cfg.Enabled || cfg.IntervalSeconds <= 0 {
		return func() {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := time.Duration(cfg.IntervalSeconds) * time.Second
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		if cfg.RunOnStart {
			runSQLiteMaintenance(runCtx, logf, store, cfg)
		}
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				runSQLiteMaintenance(runCtx, logf, store, cfg)
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func runSQLiteMaintenance(ctx context.Context, logf collector.WatchLogFunc, store *sqlite.Store, cfg appconfig.MaintenanceConfig) {
	report, err := store.Maintenance(ctx, sqlite.MaintenanceOptions{
		MinWalBytes:                 cfg.MinWalBytes,
		IncrementalVacuumPages:      cfg.IncrementalVacuumPages,
		JournalSizeLimitBytes:       cfg.JournalSizeLimitBytes,
		SkipWhenDatabaseBusy:        cfg.SkipWhenDatabaseBusy,
		LogUnchangedMaintenanceRuns: cfg.LogUnchangedMaintenanceRuns,
	})
	if err != nil {
		if logf != nil {
			logf("storage maintenance error", "error", err)
		}
		return
	}
	if logf == nil || (report.Skipped && !cfg.LogUnchangedMaintenanceRuns) {
		return
	}
	if report.BytesReclaimed == 0 && !cfg.LogUnchangedMaintenanceRuns {
		return
	}
	logf(
		"storage maintenance",
		"reclaimedBytes", report.BytesReclaimed,
		"walBefore", report.WALBytesBefore,
		"walAfter", report.WALBytesAfter,
		"freelistBefore", report.FreelistPagesBefore,
		"freelistAfter", report.FreelistPagesAfter,
		"skipped", report.Skipped,
		"reason", report.Reason,
	)
}
