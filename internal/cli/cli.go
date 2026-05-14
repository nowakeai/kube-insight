package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/benchmark"
	"kube-insight/internal/collector"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/samplegen"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

const version = "0.1.0-dev"

// Run executes the kube-insight CLI. It intentionally uses only the standard
// library until the first storage and Kubernetes dependencies are selected.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	case "version", "--version":
		fmt.Fprintf(stdout, "kube-insight %s\n", version)
		return nil
	case "ingest":
		return runIngest(ctx, args[1:], stdout)
	case "collect":
		return runCollect(ctx, args[1:], stdout)
	case "generate":
		return runGenerate(ctx, args[1:], stdout)
	case "discover":
		return runDiscover(ctx, args[1:], stdout)
	case "investigate":
		return runInvestigate(ctx, args[1:], stdout)
	case "topology":
		return runTopology(ctx, args[1:], stdout)
	case "benchmark":
		return runBenchmark(ctx, args[1:], stdout)
	case "validate":
		return runValidate(ctx, args[1:], stdout)
	case "watch":
		return runWatch(ctx, args[1:], stdout)
	case "diff":
		return fmt.Errorf("%s is not implemented yet", args[0])
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runValidate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "poc" {
		return fmt.Errorf("validate supports only the poc subcommand")
	}
	var opts benchmark.ValidationOptions
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--fixtures":
			if i+1 >= len(args) {
				return fmt.Errorf("--fixtures requires a path")
			}
			opts.FixturesDir = args[i+1]
			i++
		case "--output", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.OutputDir = args[i+1]
			i++
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.DBPath = args[i+1]
			i++
		case "--clusters":
			if i+1 >= len(args) {
				return fmt.Errorf("--clusters requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--clusters must be a positive integer")
			}
			opts.Clusters = n
			i++
		case "--copies":
			if i+1 >= len(args) {
				return fmt.Errorf("--copies requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--copies must be a positive integer")
			}
			opts.Copies = n
			i++
		case "--query-runs":
			if i+1 >= len(args) {
				return fmt.Errorf("--query-runs requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--query-runs must be a positive integer")
			}
			opts.QueryRuns = n
			i++
		case "--max-service-investigation-p95-ms":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-service-investigation-p95-ms requires a number")
			}
			n, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("--max-service-investigation-p95-ms must be a positive number")
			}
			opts.MaxServiceInvestigationP95MS = n
			i++
		case "--max-latest-lookup-p95-ms":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-latest-lookup-p95-ms requires a number")
			}
			n, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("--max-latest-lookup-p95-ms must be a positive number")
			}
			opts.MaxLatestLookupP95MS = n
			i++
		case "--max-historical-get-p95-ms":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-historical-get-p95-ms requires a number")
			}
			n, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("--max-historical-get-p95-ms must be a positive number")
			}
			opts.MaxHistoricalGetP95MS = n
			i++
		case "--max-stored-to-raw-ratio":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-stored-to-raw-ratio requires a number")
			}
			n, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("--max-stored-to-raw-ratio must be a positive number")
			}
			opts.MaxStoredToRawRatio = n
			i++
		case "--min-service-versions":
			if i+1 >= len(args) {
				return fmt.Errorf("--min-service-versions requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--min-service-versions must be a positive integer")
			}
			opts.MinServiceVersions = n
			i++
		case "--min-service-diffs":
			if i+1 >= len(args) {
				return fmt.Errorf("--min-service-diffs requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--min-service-diffs must be a positive integer")
			}
			opts.MinServiceDiffs = n
			i++
		default:
			return fmt.Errorf("unknown validate poc argument %q", args[i])
		}
	}
	report, err := benchmark.RunPoCValidation(ctx, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if !report.Passed {
		return fmt.Errorf("poc validation failed")
	}
	return nil
}

func runBenchmark(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "watch" {
		return runBenchmarkWatch(ctx, args[1:], stdout)
	}
	if len(args) > 0 && args[0] == "local" {
		args = args[1:]
	}
	var opts benchmark.Options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fixtures":
			if i+1 >= len(args) {
				return fmt.Errorf("--fixtures requires a path")
			}
			opts.FixturesDir = args[i+1]
			i++
		case "--output", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.OutputDir = args[i+1]
			i++
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.DBPath = args[i+1]
			i++
		case "--clusters":
			if i+1 >= len(args) {
				return fmt.Errorf("--clusters requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--clusters must be a positive integer")
			}
			opts.Clusters = n
			i++
		case "--copies":
			if i+1 >= len(args) {
				return fmt.Errorf("--copies requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--copies must be a positive integer")
			}
			opts.Copies = n
			i++
		case "--query-runs":
			if i+1 >= len(args) {
				return fmt.Errorf("--query-runs requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--query-runs must be a positive integer")
			}
			opts.QueryRuns = n
			i++
		default:
			return fmt.Errorf("unknown benchmark argument %q", args[i])
		}
	}
	report, err := benchmark.RunLocal(ctx, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runBenchmarkWatch(ctx context.Context, args []string, stdout io.Writer) error {
	opts := benchmark.WatchOptions{MaxRetries: -1}
	var resourceArgs []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.DBPath = args[i+1]
			i++
		case "--context":
			if i+1 >= len(args) {
				return fmt.Errorf("--context requires a name")
			}
			opts.Context = args[i+1]
			i++
		case "--namespace", "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a namespace", args[i])
			}
			opts.Namespace = args[i+1]
			i++
		case "--resource":
			if i+1 >= len(args) {
				return fmt.Errorf("--resource requires a value")
			}
			resourceArgs = append(resourceArgs, args[i+1])
			i++
		case "--discover-resources":
			opts.DiscoverResources = true
		case "--duration", "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a duration", args[i])
			}
			duration, err := time.ParseDuration(args[i+1])
			if err != nil {
				return err
			}
			opts.Duration = duration
			i++
		case "--concurrency":
			if i+1 >= len(args) {
				return fmt.Errorf("--concurrency requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--concurrency must be a positive integer")
			}
			opts.Concurrency = n
			i++
		case "--retries":
			if i+1 >= len(args) {
				return fmt.Errorf("--retries requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--retries must be a non-negative integer")
			}
			opts.MaxRetries = n
			i++
		case "--max-events":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-events requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--max-events must be a non-negative integer")
			}
			opts.MaxEvents = n
			i++
		default:
			return fmt.Errorf("unknown benchmark watch argument %q", args[i])
		}
	}
	if len(resourceArgs) == 0 && !opts.DiscoverResources {
		return fmt.Errorf("benchmark watch requires --resource or --discover-resources")
	}
	resources, err := collector.ParseResources(resourceArgs)
	if err != nil {
		return err
	}
	if len(resourceArgs) > 0 {
		opts.Resources = resources
	}
	report, err := benchmark.RunWatch(ctx, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func runDiscover(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "resources" {
		return fmt.Errorf("discover supports only the resources subcommand")
	}
	var dbPath string
	var contexts []string
	var allContexts bool
	var useClientGo bool
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--all-contexts":
			allContexts = true
		case "--client-go":
			useClientGo = true
		case "--context":
			if i+1 >= len(args) {
				return fmt.Errorf("--context requires a name")
			}
			contexts = append(contexts, args[i+1])
			i++
		default:
			return fmt.Errorf("unknown discover resources argument %q", args[i])
		}
	}
	if dbPath == "" {
		return fmt.Errorf("discover resources requires --db")
	}
	if allContexts {
		discoveredContexts, err := configuredContexts(ctx, useClientGo)
		if err != nil {
			return err
		}
		contexts = discoveredContexts
	}
	if len(contexts) == 0 {
		current, err := currentContext(ctx, useClientGo)
		if err != nil {
			return err
		}
		contexts = []string{current}
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	type contextSummary struct {
		Context   string `json:"context"`
		Resources int    `json:"resources"`
		Error     string `json:"error,omitempty"`
	}
	out := struct {
		DiscoveredAt time.Time        `json:"discoveredAt"`
		Contexts     []contextSummary `json:"contexts"`
		Resources    int              `json:"resources"`
	}{DiscoveredAt: time.Now().UTC()}

	seen := map[string]kubeapi.ResourceInfo{}
	for _, kubeContext := range contexts {
		resources, err := discoverResources(ctx, kubeContext, useClientGo)
		if err != nil {
			out.Contexts = append(out.Contexts, contextSummary{Context: kubeContext, Error: err.Error()})
			continue
		}
		infos := resourcesToAPIInfos(resources)
		for _, info := range infos {
			key := info.Group + "\x00" + info.Version + "\x00" + info.Resource
			seen[key] = info
		}
		out.Contexts = append(out.Contexts, contextSummary{Context: kubeContext, Resources: len(infos)})
	}
	infos := make([]kubeapi.ResourceInfo, 0, len(seen))
	for _, info := range seen {
		infos = append(infos, info)
	}
	if err := store.UpsertAPIResources(ctx, infos, out.DiscoveredAt); err != nil {
		return err
	}
	out.Resources = len(infos)
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func runGenerate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "samples" {
		return fmt.Errorf("generate supports only the samples subcommand")
	}
	var opts samplegen.Options
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--fixtures":
			if i+1 >= len(args) {
				return fmt.Errorf("--fixtures requires a path")
			}
			opts.FixturesDir = args[i+1]
			i++
		case "--output", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			opts.OutputDir = args[i+1]
			i++
		case "--clusters":
			if i+1 >= len(args) {
				return fmt.Errorf("--clusters requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--clusters must be a positive integer")
			}
			opts.Clusters = n
			i++
		case "--copies":
			if i+1 >= len(args) {
				return fmt.Errorf("--copies requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--copies must be a positive integer")
			}
			opts.Copies = n
			i++
		default:
			return fmt.Errorf("unknown generate samples argument %q", args[i])
		}
	}
	manifest, err := samplegen.Generate(ctx, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

func runTopology(ctx context.Context, args []string, stdout io.Writer) error {
	dbPath, target, err := parseDBTarget("topology", args)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	graph, err := store.Topology(ctx, target)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(graph)
}

func runInvestigate(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "service" {
		return runInvestigateService(ctx, args[1:], stdout)
	}
	dbPath, target, err := parseDBTarget("investigate", args)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	bundle, err := store.Investigate(ctx, target)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(bundle)
}

func runInvestigateService(ctx context.Context, args []string, stdout io.Writer) error {
	dbPath, target, opts, err := parseServiceInvestigationTarget(args)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	bundle, err := store.InvestigateServiceWithOptions(ctx, target, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(bundle)
}

func parseServiceInvestigationTarget(args []string) (string, sqlite.ObjectTarget, sqlite.InvestigationOptions, error) {
	var dbPath string
	target := sqlite.ObjectTarget{Kind: "Service"}
	var opts sqlite.InvestigationOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--cluster":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--cluster requires a value")
			}
			target.ClusterID = args[i+1]
			i++
		case "--namespace", "-n":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--namespace requires a value")
			}
			target.Namespace = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--name requires a value")
			}
			target.Name = args[i+1]
			i++
		case "--from":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--from requires a value")
			}
			t, err := parseInvestigationTime(args[i+1])
			if err != nil {
				return "", target, opts, fmt.Errorf("--from: %w", err)
			}
			opts.From = t
			i++
		case "--to":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--to requires a value")
			}
			t, err := parseInvestigationTime(args[i+1])
			if err != nil {
				return "", target, opts, fmt.Errorf("--to: %w", err)
			}
			opts.To = t
			i++
		case "--max-evidence-objects":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--max-evidence-objects requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return "", target, opts, fmt.Errorf("--max-evidence-objects must be a non-negative integer")
			}
			opts.MaxEvidenceObjects = n
			i++
		case "--max-versions-per-object":
			if i+1 >= len(args) {
				return "", target, opts, fmt.Errorf("--max-versions-per-object requires a value")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return "", target, opts, fmt.Errorf("--max-versions-per-object must be a non-negative integer")
			}
			opts.MaxVersionsPerObject = n
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", target, opts, fmt.Errorf("unknown investigate service argument %q", args[i])
			}
			if target.Name != "" {
				return "", target, opts, fmt.Errorf("unexpected investigate service argument %q", args[i])
			}
			target.Name = args[i]
		}
	}
	if dbPath == "" {
		return "", target, opts, fmt.Errorf("investigate service requires --db")
	}
	if target.Name == "" {
		return "", target, opts, fmt.Errorf("investigate service requires a service name")
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return "", target, opts, fmt.Errorf("--from must be before --to")
	}
	return dbPath, target, opts, nil
}

func parseInvestigationTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 timestamp or YYYY-MM-DD date")
}

func parseDBTarget(command string, args []string) (string, sqlite.ObjectTarget, error) {
	var dbPath string
	var target sqlite.ObjectTarget
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--cluster":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("--cluster requires a value")
			}
			target.ClusterID = args[i+1]
			i++
		case "--uid":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("--uid requires a value")
			}
			target.UID = args[i+1]
			i++
		case "--kind":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("--kind requires a value")
			}
			target.Kind = args[i+1]
			i++
		case "--namespace", "-n":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("--namespace requires a value")
			}
			target.Namespace = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return "", target, fmt.Errorf("--name requires a value")
			}
			target.Name = args[i+1]
			i++
		default:
			return "", target, fmt.Errorf("unknown %s argument %q", command, args[i])
		}
	}
	if dbPath == "" {
		return "", target, fmt.Errorf("%s requires --db", command)
	}
	return dbPath, target, nil
}

func runCollect(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("collect requires a subcommand")
	}
	switch args[0] {
	case "samples":
		return runCollectSamples(ctx, args[1:], stdout)
	case "ingest":
		return runCollectIngest(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("collect supports only samples and ingest subcommands")
	}
}

func runWatch(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("watch requires a subcommand")
	}
	switch args[0] {
	case "resource":
		return runWatchResource(ctx, args[1:], stdout)
	case "resources":
		return runWatchResources(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("watch supports only resource and resources subcommands")
	}
}

func runWatchResource(ctx context.Context, args []string, stdout io.Writer) error {
	var dbPath string
	var kubeContext string
	var namespace string
	var resourceArg string
	var maxEvents int
	maxRetries := -1
	var timeout time.Duration
	var useClientGo bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client-go":
			useClientGo = true
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--context":
			if i+1 >= len(args) {
				return fmt.Errorf("--context requires a name")
			}
			kubeContext = args[i+1]
			i++
		case "--namespace", "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a namespace", args[i])
			}
			namespace = args[i+1]
			i++
		case "--resource":
			if i+1 >= len(args) {
				return fmt.Errorf("--resource requires a value")
			}
			resourceArg = args[i+1]
			i++
		case "--max-events":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-events requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--max-events must be a non-negative integer")
			}
			maxEvents = n
			i++
		case "--retries":
			if i+1 >= len(args) {
				return fmt.Errorf("--retries requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--retries must be a non-negative integer")
			}
			maxRetries = n
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("--timeout requires a duration")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return err
			}
			timeout = d
			i++
		default:
			return fmt.Errorf("unknown watch resource argument %q", args[i])
		}
	}
	if !useClientGo {
		return fmt.Errorf("watch resource currently requires --client-go")
	}
	if dbPath == "" {
		return fmt.Errorf("watch resource requires --db")
	}
	if resourceArg == "" {
		return fmt.Errorf("watch resource requires --resource")
	}
	resources, err := collector.ParseResources([]string{resourceArg})
	if err != nil {
		return err
	}
	if len(resources) != 1 {
		return fmt.Errorf("watch resource requires exactly one resource")
	}
	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer sqliteStore.Close()

	summary, err := collector.WatchResourceClientGo(ctx, collector.WatchOptions{
		Context:    kubeContext,
		Resource:   resources[0],
		Namespace:  namespace,
		Store:      sqliteStore,
		MaxEvents:  maxEvents,
		Timeout:    timeout,
		MaxRetries: maxRetries,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

func runWatchResources(ctx context.Context, args []string, stdout io.Writer) error {
	var dbPath string
	var kubeContext string
	var namespace string
	var resourceArgs []string
	var maxEvents int
	maxRetries := -1
	var concurrency int
	var timeout time.Duration
	var useClientGo bool
	var discoverResources bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client-go":
			useClientGo = true
		case "--discover-resources":
			discoverResources = true
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--context":
			if i+1 >= len(args) {
				return fmt.Errorf("--context requires a name")
			}
			kubeContext = args[i+1]
			i++
		case "--namespace", "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a namespace", args[i])
			}
			namespace = args[i+1]
			i++
		case "--resource":
			if i+1 >= len(args) {
				return fmt.Errorf("--resource requires a value")
			}
			resourceArgs = append(resourceArgs, args[i+1])
			i++
		case "--max-events":
			if i+1 >= len(args) {
				return fmt.Errorf("--max-events requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--max-events must be a non-negative integer")
			}
			maxEvents = n
			i++
		case "--retries":
			if i+1 >= len(args) {
				return fmt.Errorf("--retries requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				return fmt.Errorf("--retries must be a non-negative integer")
			}
			maxRetries = n
			i++
		case "--concurrency":
			if i+1 >= len(args) {
				return fmt.Errorf("--concurrency requires a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Errorf("--concurrency must be a positive integer")
			}
			concurrency = n
			i++
		case "--timeout":
			if i+1 >= len(args) {
				return fmt.Errorf("--timeout requires a duration")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return err
			}
			timeout = d
			i++
		default:
			return fmt.Errorf("unknown watch resources argument %q", args[i])
		}
	}
	if !useClientGo {
		return fmt.Errorf("watch resources currently requires --client-go")
	}
	if dbPath == "" {
		return fmt.Errorf("watch resources requires --db")
	}
	if len(resourceArgs) == 0 && !discoverResources {
		return fmt.Errorf("watch resources requires --resource or --discover-resources")
	}
	resources, err := collector.ParseResources(resourceArgs)
	if err != nil {
		return err
	}
	if len(resourceArgs) == 0 {
		resources = nil
	}
	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer sqliteStore.Close()

	summary, err := collector.WatchResourcesClientGo(ctx, collector.WatchResourcesOptions{
		Context:           kubeContext,
		Resources:         resources,
		DiscoverResources: discoverResources,
		Namespace:         namespace,
		Store:             sqliteStore,
		MaxEvents:         maxEvents,
		Timeout:           timeout,
		Concurrency:       concurrency,
		MaxRetries:        maxRetries,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

func runCollectSamples(ctx context.Context, args []string, stdout io.Writer) error {
	opts, _, err := parseCollectOptions(ctx, args)
	if err != nil {
		return err
	}
	manifest, err := collector.CollectSamples(ctx, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

func runCollectIngest(ctx context.Context, args []string, stdout io.Writer) error {
	opts, dbPath, err := parseCollectOptions(ctx, args)
	if err != nil {
		return err
	}
	if dbPath == "" {
		return fmt.Errorf("collect ingest requires --db")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("testdata", "generated", "collect-ingest")
	}

	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		return err
	}
	defer sqliteStore.Close()

	manifest, err := collector.CollectSamples(ctx, opts)
	if err != nil {
		return err
	}
	summary, err := ingestInputs(ctx, sqliteStore, "", opts.OutputDir)
	if err != nil {
		return err
	}

	out := struct {
		Manifest collector.SampleManifest `json:"manifest"`
		Summary  ingest.Summary           `json:"summary"`
	}{
		Manifest: manifest,
		Summary:  summary,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func parseCollectOptions(ctx context.Context, args []string) (collector.SampleOptions, string, error) {
	var opts collector.SampleOptions
	var resourceArgs []string
	var dbPath string
	var useDBResources bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			if i+1 >= len(args) {
				return opts, "", fmt.Errorf("%s requires a path", args[i])
			}
			opts.OutputDir = args[i+1]
			i++
		case "--all-contexts":
			opts.AllContexts = true
		case "--discover-resources":
			opts.DiscoverResources = true
		case "--client-go":
			opts.UseClientGo = true
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return opts, "", fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		case "--use-db-resources":
			useDBResources = true
		case "--context":
			if i+1 >= len(args) {
				return opts, "", fmt.Errorf("--context requires a name")
			}
			opts.Contexts = append(opts.Contexts, args[i+1])
			i++
		case "--resource":
			if i+1 >= len(args) {
				return opts, "", fmt.Errorf("--resource requires a value")
			}
			resourceArgs = append(resourceArgs, args[i+1])
			i++
		case "--max-items":
			if i+1 >= len(args) {
				return opts, "", fmt.Errorf("--max-items requires a number")
			}
			maxItems, err := strconv.Atoi(args[i+1])
			if err != nil || maxItems <= 0 {
				return opts, "", fmt.Errorf("--max-items must be a positive integer")
			}
			opts.MaxItems = maxItems
			i++
		default:
			return opts, "", fmt.Errorf("unknown collect argument %q", args[i])
		}
	}
	resources, err := collector.ParseResources(resourceArgs)
	if err != nil {
		return opts, "", err
	}
	opts.Resources = resources
	if useDBResources && dbPath == "" {
		return opts, "", fmt.Errorf("--use-db-resources requires --db")
	}
	if dbPath != "" {
		sqliteStore, err := sqlite.Open(dbPath)
		if err != nil {
			return opts, "", err
		}
		defer sqliteStore.Close()

		if opts.DiscoverResources {
			contexts, err := collectContexts(ctx, opts)
			if err != nil {
				return opts, "", err
			}
			discovered, err := discoverAndStoreAPIResources(ctx, sqliteStore, contexts, opts.UseClientGo)
			if err != nil {
				return opts, "", err
			}
			if len(resourceArgs) == 0 {
				opts.Resources = discovered
			} else {
				opts.Resources = enrichCollectorResources(opts.Resources, discovered)
			}
			opts.DiscoverResources = false
		}
		if useDBResources {
			infos, err := sqliteStore.APIResources(ctx)
			if err != nil {
				return opts, "", err
			}
			dbResources := apiInfosToResources(infos)
			if len(dbResources) == 0 {
				return opts, "", fmt.Errorf("no listable API resources found in --db")
			}
			if len(resourceArgs) == 0 {
				opts.Resources = dbResources
			} else {
				opts.Resources = enrichCollectorResources(opts.Resources, dbResources)
			}
			opts.DiscoverResources = false
		}
	}
	return opts, dbPath, nil
}

func collectContexts(ctx context.Context, opts collector.SampleOptions) ([]string, error) {
	if opts.AllContexts {
		return configuredContexts(ctx, opts.UseClientGo)
	}
	if len(opts.Contexts) > 0 {
		return opts.Contexts, nil
	}
	current, err := currentContext(ctx, opts.UseClientGo)
	if err != nil {
		return nil, err
	}
	return []string{current}, nil
}

func discoverAndStoreAPIResources(ctx context.Context, store storage.APIResourceStore, contexts []string, useClientGo bool) ([]collector.Resource, error) {
	seen := map[string]collector.Resource{}
	for _, kubeContext := range contexts {
		resources, err := discoverResources(ctx, kubeContext, useClientGo)
		if err != nil {
			return nil, err
		}
		if err := store.UpsertAPIResources(ctx, resourcesToAPIInfos(resources), time.Now().UTC()); err != nil {
			return nil, err
		}
		for _, resource := range resources {
			seen[collectorResourceKey(resource)] = resource
		}
	}
	out := make([]collector.Resource, 0, len(seen))
	for _, resource := range seen {
		out = append(out, resource)
	}
	sort.Slice(out, func(i, j int) bool { return collectorResourceKey(out[i]) < collectorResourceKey(out[j]) })
	return out, nil
}

func discoverResources(ctx context.Context, kubeContext string, useClientGo bool) ([]collector.Resource, error) {
	if useClientGo {
		return collector.DiscoverResourcesClientGo(ctx, kubeContext)
	}
	return collector.DiscoverResources(ctx, kubeContext)
}

func configuredContexts(ctx context.Context, useClientGo bool) ([]string, error) {
	if useClientGo {
		return collector.ConfiguredContextsClientGo()
	}
	return collector.ConfiguredContexts(ctx)
}

func currentContext(ctx context.Context, useClientGo bool) (string, error) {
	if useClientGo {
		return collector.CurrentContextClientGo()
	}
	return collector.CurrentContext(ctx)
}

func resourcesToAPIInfos(resources []collector.Resource) []kubeapi.ResourceInfo {
	out := make([]kubeapi.ResourceInfo, 0, len(resources))
	for _, resource := range resources {
		if resource.Resource == "" || resource.Kind == "" {
			continue
		}
		out = append(out, kubeapi.ResourceInfo{
			Group:      resource.Group,
			Version:    resource.Version,
			Resource:   resource.Resource,
			Kind:       resource.Kind,
			Namespaced: resource.Namespaced,
			Verbs:      resource.Verbs,
		})
	}
	return out
}

func apiInfosToResources(infos []kubeapi.ResourceInfo) []collector.Resource {
	out := make([]collector.Resource, 0, len(infos))
	for _, info := range infos {
		if info.Resource == "" {
			continue
		}
		if len(info.Verbs) > 0 && !hasVerb(info.Verbs, "list") {
			continue
		}
		name := info.Resource
		if info.Group != "" {
			name += "." + info.Group
		}
		out = append(out, collector.Resource{
			Name:       name,
			Group:      info.Group,
			Version:    info.Version,
			Resource:   info.Resource,
			Kind:       info.Kind,
			Namespaced: info.Namespaced,
			Verbs:      info.Verbs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return collectorResourceKey(out[i]) < collectorResourceKey(out[j]) })
	return out
}

func mergeCollectorResources(groups ...[]collector.Resource) []collector.Resource {
	seen := map[string]bool{}
	var out []collector.Resource
	for _, group := range groups {
		for _, resource := range group {
			key := collectorResourceKey(resource)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, resource)
		}
	}
	return out
}

func enrichCollectorResources(base, metadata []collector.Resource) []collector.Resource {
	byKey := map[string]collector.Resource{}
	for _, resource := range metadata {
		byKey[collectorResourceKey(resource)] = resource
	}
	out := make([]collector.Resource, len(base))
	for i, resource := range base {
		if enriched, ok := byKey[collectorResourceKey(resource)]; ok {
			out[i] = enriched
			continue
		}
		out[i] = resource
	}
	return out
}

func collectorResourceKey(resource collector.Resource) string {
	return resource.Name + "\x00" + strconv.FormatBool(resource.Namespaced)
}

func hasVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if strings.Trim(verb, "[],") == want {
			return true
		}
	}
	return false
}

func runIngest(ctx context.Context, args []string, stdout io.Writer) error {
	var file string
	var dir string
	var dbPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file", "-f":
			if i+1 >= len(args) {
				return fmt.Errorf("--file requires a path")
			}
			file = args[i+1]
			i++
		case "--dir", "-d":
			if i+1 >= len(args) {
				return fmt.Errorf("--dir requires a path")
			}
			dir = args[i+1]
			i++
		case "--db", "--sqlite":
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a path", args[i])
			}
			dbPath = args[i+1]
			i++
		default:
			return fmt.Errorf("unknown ingest argument %q", args[i])
		}
	}
	if (file == "") == (dir == "") {
		return fmt.Errorf("ingest requires exactly one of --file or --dir")
	}

	store := storage.Store(storage.NewMemoryStore())
	if dbPath != "" {
		sqliteStore, err := sqlite.Open(dbPath)
		if err != nil {
			return err
		}
		defer sqliteStore.Close()
		store = sqliteStore
	}
	summary, err := ingestInputs(ctx, store, file, dir)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

func ingestInputs(ctx context.Context, store storage.Store, file, dir string) (ingest.Summary, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return ingest.Summary{}, err
		}
		pipeline := ingest.DefaultPipeline(store)
		return pipeline.IngestJSON(ctx, data)
	}

	files, err := jsonFiles(dir)
	if err != nil {
		return ingest.Summary{}, err
	}
	var total ingest.Summary
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return total, err
		}
		pipeline := ingest.DefaultPipeline(store)
		pipeline.ClusterID = clusterIDForPath(dir, path)
		summary, err := pipeline.IngestJSON(ctx, data)
		if err != nil {
			return total, fmt.Errorf("%s: %w", path, err)
		}
		total.Observations += summary.Observations
		total.StoredObservations += summary.StoredObservations
		total.ModifiedObservations += summary.ModifiedObservations
		total.DiscardedChanges += summary.DiscardedChanges
		total.DiscardedResources += summary.DiscardedResources
		total.Facts += summary.Facts
		total.Edges += summary.Edges
		total.Changes += summary.Changes
	}
	return total, nil
}

func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == "manifest.json" {
			return nil
		}
		if filepath.Ext(path) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no JSON files found in %s", dir)
	}
	return files, nil
}

func clusterIDForPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "local"
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 1 && parts[0] != "" {
		return parts[0]
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, "cluster-") {
		return base
	}
	return "local"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace(`
kube-insight records Kubernetes resource history and extracts evidence.

Usage:
  kube-insight help
  kube-insight version
  kube-insight collect samples --all-contexts --discover-resources --db kube-insight.db --output testdata/kube-samples
  kube-insight collect samples --client-go --all-contexts --discover-resources --db kube-insight.db --output testdata/kube-samples
  kube-insight collect samples --use-db-resources --db kube-insight.db --output testdata/kube-samples
  kube-insight collect ingest --context staging --discover-resources --db kube-insight.db --output testdata/generated/live-samples
  kube-insight collect ingest --client-go --context staging --discover-resources --db kube-insight.db --output testdata/generated/live-samples
  kube-insight discover resources --db kube-insight.db [--context staging]
  kube-insight discover resources --client-go --db kube-insight.db [--context staging]
  kube-insight generate samples --fixtures testdata/fixtures/kube --output testdata/generated/kube-samples
  kube-insight ingest --file sample.json [--db kube-insight.db]
  kube-insight ingest --dir testdata/kube-samples [--db kube-insight.db]
  kube-insight investigate --db kube-insight.db --kind Pod --namespace default --name api
  kube-insight investigate service api --namespace default --db kube-insight.db [--from 2026-05-14T10:00:00Z --to 2026-05-14T11:00:00Z] [--max-evidence-objects 5 --max-versions-per-object 3]
  kube-insight topology --db kube-insight.db --kind Pod --namespace default --name api
  kube-insight benchmark local --fixtures testdata/fixtures/kube --output testdata/generated/benchmark-samples --db kube-insight.db --query-runs 25
  kube-insight benchmark watch --db kube-insight-watch.db --context staging --resource pods --resource services --duration 30s --concurrency 2 --retries 3
  kube-insight validate poc --fixtures testdata/fixtures/kube --output testdata/generated/poc-validation --db kube-insight-poc-validation.db
  kube-insight watch resource --client-go --db kube-insight.db --context staging --resource pods --max-events 1 --timeout 30s --retries 3
  kube-insight watch resources --client-go --db kube-insight.db --context staging --resource pods --resource services --concurrency 2 --timeout 30s --retries 3
  kube-insight diff         # planned
`))
}
