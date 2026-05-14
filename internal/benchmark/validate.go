package benchmark

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"kube-insight/internal/collector"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

type ValidationOptions struct {
	Options
	MaxServiceInvestigationP95MS float64
	MaxLatestLookupP95MS         float64
	MaxHistoricalGetP95MS        float64
	MaxStoredToRawRatio          float64
	MinServiceVersions           int
	MinServiceDiffs              int
}

type ValidationReport struct {
	Passed      bool                               `json:"passed"`
	Summary     ValidationSummary                  `json:"summary"`
	SummaryText string                             `json:"summaryText"`
	Checks      []ValidationCheck                  `json:"checks"`
	Target      sqlite.ObjectTarget                `json:"target"`
	Service     sqlite.ServiceInvestigationSummary `json:"service"`
	FakeWatch   collector.WatchResourcesSummary    `json:"fakeWatch"`
	Report      Report                             `json:"report"`
}

type ValidationSummary struct {
	Status                    string  `json:"status"`
	ChecksPassed              int     `json:"checksPassed"`
	ChecksFailed              int     `json:"checksFailed"`
	Objects                   int64   `json:"objects"`
	Versions                  int64   `json:"versions"`
	StoredToRawRatio          float64 `json:"storedToRawRatio"`
	LatestLookupP95MS         float64 `json:"latestLookupP95Ms"`
	HistoricalGetP95MS        float64 `json:"historicalGetP95Ms"`
	ServiceInvestigationP95MS float64 `json:"serviceInvestigationP95Ms"`
	ServiceVersions           int     `json:"serviceVersions"`
	ServiceDiffs              int     `json:"serviceDiffs"`
	RelistConfirmedDeletes    int     `json:"relistConfirmedDeletes"`
	UnknownVisibility         int     `json:"unknownVisibility"`
	BackpressureEvents        int     `json:"backpressureEvents"`
}

type ValidationCheck struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

func RunPoCValidation(ctx context.Context, opts ValidationOptions) (ValidationReport, error) {
	if opts.Clusters <= 0 {
		opts.Clusters = 1
	}
	if opts.Copies <= 0 {
		opts.Copies = 2
	}
	if opts.QueryRuns <= 0 {
		opts.QueryRuns = 3
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("testdata", "generated", "poc-validation")
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(opts.OutputDir, "kube-insight-poc-validation.db")
	}
	if opts.MaxServiceInvestigationP95MS <= 0 {
		opts.MaxServiceInvestigationP95MS = 5000
	}
	if opts.MaxLatestLookupP95MS <= 0 {
		opts.MaxLatestLookupP95MS = 250
	}
	if opts.MaxHistoricalGetP95MS <= 0 {
		opts.MaxHistoricalGetP95MS = 250
	}
	if opts.MaxStoredToRawRatio <= 0 {
		opts.MaxStoredToRawRatio = 20
	}
	if opts.MinServiceVersions <= 0 {
		opts.MinServiceVersions = 3
	}
	if opts.MinServiceDiffs <= 0 {
		opts.MinServiceDiffs = 1
	}

	report, err := RunLocal(ctx, opts.Options)
	if err != nil {
		return ValidationReport{}, err
	}
	out := ValidationReport{Passed: true, Report: report}
	out.add("objects stored", report.Objects > 0, "> 0", fmt.Sprintf("%d", report.Objects))
	out.add("multi-version history stored", report.Versions > report.Counts.LatestRows, "versions > latest rows", fmt.Sprintf("%d > %d", report.Versions, report.Counts.LatestRows))
	out.add("resource processing profiles recorded", report.ProcessingProfiles > 0, "> 0", fmt.Sprintf("%d", report.ProcessingProfiles))
	out.add("secret payload safety", report.SecretPayloadViolations == 0, "0 violations", fmt.Sprintf("%d", report.SecretPayloadViolations))
	out.add("secret redaction happened", report.RedactedFields > 0, "> 0 redacted fields", fmt.Sprintf("%d", report.RedactedFields))
	out.add("event facts indexed", report.EventFactRows > 0 && report.EventMessageFingerprints > 0, "event facts and fingerprints > 0", fmt.Sprintf("facts=%d fingerprints=%d", report.EventFactRows, report.EventMessageFingerprints))
	out.add("pod status changes indexed", report.PodStatusChangeRows > 0, "> 0", fmt.Sprintf("%d", report.PodStatusChangeRows))
	out.add("node condition facts indexed", report.NodeConditionFactRows > 0, "> 0", fmt.Sprintf("%d", report.NodeConditionFactRows))
	out.add("endpointslice readiness facts indexed", report.EndpointSliceReadinessFactRows > 0, "> 0", fmt.Sprintf("%d", report.EndpointSliceReadinessFactRows))
	storedToRawRatio := storageGrowthRatio(report)
	out.add("storage growth bounded", storedToRawRatio > 0 && storedToRawRatio <= opts.MaxStoredToRawRatio, fmt.Sprintf("> 0 and <= %.1fx raw", opts.MaxStoredToRawRatio), fmt.Sprintf("%.2fx raw", storedToRawRatio))
	out.add("latest lookup latency bounded", report.LatestLookupP95MS > 0 && report.LatestLookupP95MS <= opts.MaxLatestLookupP95MS, fmt.Sprintf("> 0 and <= %.0f ms", opts.MaxLatestLookupP95MS), fmt.Sprintf("%.3f ms", report.LatestLookupP95MS))
	out.add("historical get latency bounded", report.HistoricalGetP95MS > 0 && report.HistoricalGetP95MS <= opts.MaxHistoricalGetP95MS, fmt.Sprintf("> 0 and <= %.0f ms", opts.MaxHistoricalGetP95MS), fmt.Sprintf("%.3f ms", report.HistoricalGetP95MS))
	out.add("service investigation latency", report.ServiceInvestigationP95MS > 0 && report.ServiceInvestigationP95MS <= opts.MaxServiceInvestigationP95MS, fmt.Sprintf("> 0 and <= %.0f ms", opts.MaxServiceInvestigationP95MS), fmt.Sprintf("%.3f ms", report.ServiceInvestigationP95MS))
	out.add("service versions reconstructed", report.ServiceInvestigationVersions >= opts.MinServiceVersions, fmt.Sprintf(">= %d", opts.MinServiceVersions), fmt.Sprintf("%d", report.ServiceInvestigationVersions))
	out.add("service version diffs produced", report.ServiceInvestigationDiffs >= opts.MinServiceDiffs, fmt.Sprintf(">= %d", opts.MinServiceDiffs), fmt.Sprintf("%d", report.ServiceInvestigationDiffs))

	store, err := sqlite.Open(opts.DBPath)
	if err != nil {
		return ValidationReport{}, err
	}
	defer store.Close()
	target, err := store.LatestObjectTarget(ctx, "Service")
	if err != nil {
		return ValidationReport{}, err
	}
	out.Target = target
	service, err := store.InvestigateServiceWithOptions(ctx, target, sqlite.InvestigationOptions{
		MaxEvidenceObjects:   5,
		MaxVersionsPerObject: 3,
	})
	if err != nil {
		return ValidationReport{}, err
	}
	out.Service = service.Summary
	out.add("service topology includes endpointslices and pods", service.Summary.EndpointSlices > 0 && service.Summary.Pods > 0, "endpointSlices > 0 and pods > 0", fmt.Sprintf("endpointSlices=%d pods=%d", service.Summary.EndpointSlices, service.Summary.Pods))
	out.add("service investigation has facts and changes", service.Summary.Facts > 0 && service.Summary.Changes > 0, "facts > 0 and changes > 0", fmt.Sprintf("facts=%d changes=%d", service.Summary.Facts, service.Summary.Changes))
	out.add("service investigation proves versions and diffs", service.Summary.Versions > 0 && service.Summary.VersionDiffs > 0, "versions > 0 and versionDiffs > 0", fmt.Sprintf("versions=%d versionDiffs=%d", service.Summary.Versions, service.Summary.VersionDiffs))

	fakeWatch, err := collector.RunFakeWatchReconciliation(ctx, collector.FakeWatchReconciliationOptions{
		Context:   "fake-validation",
		Store:     storage.NewMemoryStore(),
		Namespace: "default",
	})
	if err != nil {
		return ValidationReport{}, err
	}
	out.FakeWatch = fakeWatch
	out.add("fake multi-gvr relist deletes", fakeWatch.ReconciledDeleted > 0, "> 0", fmt.Sprintf("%d", fakeWatch.ReconciledDeleted))
	out.add("fake multi-gvr unknown visibility", fakeWatch.UnknownVisibility > 0, "> 0", fmt.Sprintf("%d", fakeWatch.UnknownVisibility))
	out.add("fake per-gvr queue metrics", len(fakeWatch.ResourceQueue) > 0 && fakeWatch.BackpressureEvents > 0, "resourceQueue > 0 and backpressureEvents > 0", fmt.Sprintf("resourceQueue=%d backpressureEvents=%d", len(fakeWatch.ResourceQueue), fakeWatch.BackpressureEvents))
	out.add("fake per-priority queue metrics", resourceQueueHasPriorities(fakeWatch.ResourceQueue), "resourceQueue priorities present", fmt.Sprintf("priorities=%s", resourceQueuePriorities(fakeWatch.ResourceQueue)))
	out.finalize()
	return out, nil
}

func (r *ValidationReport) add(name string, passed bool, expected, actual string) {
	r.Checks = append(r.Checks, ValidationCheck{
		Name:     name,
		Passed:   passed,
		Expected: expected,
		Actual:   actual,
	})
	if !passed {
		r.Passed = false
	}
}

func (r *ValidationReport) finalize() {
	status := "pass"
	for _, check := range r.Checks {
		if check.Passed {
			r.Summary.ChecksPassed++
		} else {
			r.Summary.ChecksFailed++
			status = "fail"
		}
	}
	r.Summary.Status = status
	r.Summary.Objects = r.Report.Objects
	r.Summary.Versions = r.Report.Versions
	r.Summary.StoredToRawRatio = storageGrowthRatio(r.Report)
	r.Summary.LatestLookupP95MS = r.Report.LatestLookupP95MS
	r.Summary.HistoricalGetP95MS = r.Report.HistoricalGetP95MS
	r.Summary.ServiceInvestigationP95MS = r.Report.ServiceInvestigationP95MS
	r.Summary.ServiceVersions = r.Report.ServiceInvestigationVersions
	r.Summary.ServiceDiffs = r.Report.ServiceInvestigationDiffs
	r.Summary.RelistConfirmedDeletes = r.FakeWatch.ReconciledDeleted
	r.Summary.UnknownVisibility = r.FakeWatch.UnknownVisibility
	r.Summary.BackpressureEvents = r.FakeWatch.BackpressureEvents
	r.SummaryText = fmt.Sprintf(
		"%s checks=%d/%d objects=%d versions=%d storage=%.2fx latest_p95=%.3fms historical_p95=%.3fms service_p95=%.3fms service_versions=%d service_diffs=%d relist_deletes=%d unknown_visibility=%d backpressure=%d",
		strings.ToUpper(status),
		r.Summary.ChecksPassed,
		len(r.Checks),
		r.Summary.Objects,
		r.Summary.Versions,
		r.Summary.StoredToRawRatio,
		r.Summary.LatestLookupP95MS,
		r.Summary.HistoricalGetP95MS,
		r.Summary.ServiceInvestigationP95MS,
		r.Summary.ServiceVersions,
		r.Summary.ServiceDiffs,
		r.Summary.RelistConfirmedDeletes,
		r.Summary.UnknownVisibility,
		r.Summary.BackpressureEvents,
	)
}

func storageGrowthRatio(report Report) float64 {
	if report.RawBytes <= 0 {
		return 0
	}
	return float64(report.StoredBytes) / float64(report.RawBytes)
}

func resourceQueueHasPriorities(queue []collector.WatchResourceQueue) bool {
	if len(queue) == 0 {
		return false
	}
	for _, item := range queue {
		if item.Priority == "" {
			return false
		}
	}
	return true
}

func resourceQueuePriorities(queue []collector.WatchResourceQueue) string {
	seen := map[string]bool{}
	var priorities []string
	for _, item := range queue {
		if item.Priority == "" || seen[item.Priority] {
			continue
		}
		seen[item.Priority] = true
		priorities = append(priorities, item.Priority)
	}
	return strings.Join(priorities, ",")
}
