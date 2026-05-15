package cli

import (
	"context"
	"sync"
	"time"

	"kube-insight/internal/collector"
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/storage/sqlite"
)

func startSQLiteMaintenanceLoop(ctx context.Context, logf collector.WatchLogFunc, store *sqlite.Store, cfg appconfig.StorageConfig) func() {
	if store == nil || !cfg.Maintenance.Enabled || cfg.Maintenance.IntervalSeconds <= 0 {
		return func() {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := time.Duration(cfg.Maintenance.IntervalSeconds) * time.Second
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		if cfg.Maintenance.RunOnStart {
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

func runSQLiteMaintenance(ctx context.Context, logf collector.WatchLogFunc, store *sqlite.Store, cfg appconfig.StorageConfig) {
	runSQLiteRetention(ctx, logf, store, cfg)
	report, err := store.Maintenance(ctx, sqlite.MaintenanceOptions{
		MinWalBytes:                 cfg.Maintenance.MinWalBytes,
		IncrementalVacuumPages:      cfg.Maintenance.IncrementalVacuumPages,
		JournalSizeLimitBytes:       cfg.Maintenance.JournalSizeLimitBytes,
		SkipWhenDatabaseBusy:        cfg.Maintenance.SkipWhenDatabaseBusy,
		LogUnchangedMaintenanceRuns: cfg.Maintenance.LogUnchangedMaintenanceRuns,
	})
	if err != nil {
		if logf != nil {
			logf("storage maintenance error", "error", err)
		}
		return
	}
	if logf == nil || (report.Skipped && !cfg.Maintenance.LogUnchangedMaintenanceRuns) {
		return
	}
	if report.BytesReclaimed == 0 && !cfg.Maintenance.LogUnchangedMaintenanceRuns {
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

func runSQLiteRetention(ctx context.Context, logf collector.WatchLogFunc, store *sqlite.Store, cfg appconfig.StorageConfig) {
	retention := cfg.Retention
	if !retention.Enabled {
		return
	}
	report, err := store.ApplyRetention(ctx, sqlite.RetentionOptions{
		MaxAge:               time.Duration(retention.MaxAgeSeconds) * time.Second,
		MinVersionsPerObject: retention.MinVersionsPerObject,
		FilterDecisionMaxAge: time.Duration(retention.FilterDecisionMaxAgeSeconds) * time.Second,
		Rules:                retentionPolicyRulesFromConfig(retention.Policies),
		SkipWhenDatabaseBusy: cfg.Maintenance.SkipWhenDatabaseBusy,
	})
	if err != nil {
		if logf != nil {
			logf("storage retention error", "error", err)
		}
		return
	}
	if logf == nil || (report.Skipped && !cfg.Maintenance.LogUnchangedMaintenanceRuns) {
		return
	}
	if report.Versions == 0 && report.Observations == 0 && report.FilterDecisions == 0 && !cfg.Maintenance.LogUnchangedMaintenanceRuns {
		return
	}
	logf(
		"storage retention",
		"versions", report.Versions,
		"observations", report.Observations,
		"facts", report.Facts,
		"changes", report.Changes,
		"edges", report.Edges,
		"blobs", report.Blobs,
		"filterDecisions", report.FilterDecisions,
		"skipped", report.Skipped,
		"reason", report.Reason,
	)
}

func retentionPolicyRulesFromConfig(policies map[string]appconfig.RetentionPolicyConfig) []sqlite.RetentionRuleOptions {
	out := make([]sqlite.RetentionRuleOptions, 0, len(policies))
	for name, policy := range policies {
		if policy.MaxAgeSeconds <= 0 {
			continue
		}
		out = append(out, sqlite.RetentionRuleOptions{
			Name:                 name,
			RetentionPolicies:    []string{name},
			MaxAge:               time.Duration(policy.MaxAgeSeconds) * time.Second,
			MinVersionsPerObject: policy.MinVersionsPerObject,
		})
	}
	return out
}
