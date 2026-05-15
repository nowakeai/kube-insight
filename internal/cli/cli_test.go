package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/collector"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"version"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "kube-insight") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRootHelpIsGrouped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"config", "db", "dev", "query", "watch"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	for _, hidden := range []string{"benchmark", "collect", "discover", "generate", "ingest", "investigate", "topology", "validate", "completion"} {
		if strings.Contains(out, "  "+hidden+" ") {
			t.Fatalf("root help exposed %q: %s", hidden, out)
		}
	}
}

func TestRunIngestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod.json")
	err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "spec": {"nodeName": "node-a"},
	  "status": {"phase": "Running"}
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{"ingest", "--file", path}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"StoredObservations": 1`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunIngestFileWithSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod.json")
	dbPath := filepath.Join(dir, "kube-insight.db")
	err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "spec": {"nodeName": "node-a"},
	  "status": {"phase": "Running"}
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{"ingest", "--file", path, "--db", dbPath}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"StoredObservations": 1`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}

	var investigation bytes.Buffer
	err = Run(context.Background(), []string{
		"investigate",
		"--db", dbPath,
		"--kind", "Pod",
		"--namespace", "default",
		"--name", "api-1",
	}, &investigation, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(investigation.String(), `"kind": "Pod"`) {
		t.Fatalf("investigation = %s", investigation.String())
	}
	if !strings.Contains(investigation.String(), `"facts": 2`) {
		t.Fatalf("investigation = %s", investigation.String())
	}

	var topology bytes.Buffer
	err = Run(context.Background(), []string{
		"topology",
		"--db", dbPath,
		"--kind", "Pod",
		"--namespace", "default",
		"--name", "api-1",
	}, &topology, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(topology.String(), `"type": "pod_on_node"`) {
		t.Fatalf("topology = %s", topology.String())
	}
}

func TestRunInvestigateServiceWithSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "incident.json")
	dbPath := filepath.Join(dir, "kube-insight.db")
	err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "api", "namespace": "default", "uid": "svc-uid"},
	      "spec": {"selector": {"app": "api"}}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	      "status": {
	        "conditions": [{"type": "Ready", "status": "False"}],
	        "containerStatuses": [{"name": "api", "restartCount": 1, "lastState": {"terminated": {"reason": "OOMKilled"}}}]
	      }
	    },
	    {
	      "apiVersion": "discovery.k8s.io/v1",
	      "kind": "EndpointSlice",
	      "metadata": {"name": "api-abc", "namespace": "default", "uid": "eps-uid", "labels": {"kubernetes.io/service-name": "api"}},
	      "endpoints": [{"targetRef": {"kind": "Pod", "name": "api-1"}, "conditions": {"ready": false}}]
	    }
	  ]
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"ingest", "--file", path, "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var investigation bytes.Buffer
	err = Run(context.Background(), []string{
		"investigate", "service", "api",
		"--namespace", "default",
		"--db", dbPath,
		"--from", "1970-01-01",
		"--to", "2100-01-01",
		"--max-evidence-objects", "2",
		"--max-versions-per-object", "1",
	}, &investigation, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"kind": "Service"`,
		`"endpointSlices": 1`,
		`"pods": 1`,
		`"pod_status.last_reason"`,
		`"evidenceScore":`,
		`"rank": 1`,
		`"versions": [`,
		`"documentHash":`,
	} {
		if !strings.Contains(investigation.String(), want) {
			t.Fatalf("investigation missing %q: %s", want, investigation.String())
		}
	}
}

func TestRunQuerySearchWithSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "incident.json")
	dbPath := filepath.Join(dir, "kube-insight.db")
	err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "policy-webhook", "namespace": "policy-system", "uid": "svc-policy-webhook"}
	    },
	    {
	      "apiVersion": "admissionregistration.k8s.io/v1",
	      "kind": "ValidatingWebhookConfiguration",
	      "metadata": {"name": "policy-guard", "uid": "vwc-policy-guard", "resourceVersion": "3"},
	      "webhooks": [{
	        "name": "policy-guard.platform.example.com",
	        "failurePolicy": "Fail",
	        "clientConfig": {"service": {"name": "policy-webhook", "namespace": "policy-system"}}
	      }]
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "webapp-admission-failed", "namespace": "flux-system", "uid": "event-webapp-admission", "resourceVersion": "4"},
	      "reason": "ReconciliationFailed",
	      "type": "Warning",
	      "message": "dry-run failed: failed calling webhook policy-guard.platform.example.com: no endpoints available for service policy-system/policy-webhook",
	      "count": 4
	    }
	  ]
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"ingest", "--file", path, "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var search bytes.Buffer
	err = Run(context.Background(), []string{
		"query", "search", "no", "endpoints",
		"--db", dbPath,
		"--limit", "5",
	}, &search, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"matches": 1`,
		`"coverage": {`,
		`"includeHealth": true`,
		`"kind": "Event"`,
		`"latest_json"`,
	} {
		if !strings.Contains(search.String(), want) {
			t.Fatalf("search missing %q: %s", want, search.String())
		}
	}
	if strings.Contains(search.String(), `"bundles": [`) {
		t.Fatalf("default search should be lightweight: %s", search.String())
	}

	search.Reset()
	err = Run(context.Background(), []string{
		"query", "search", "Fail",
		"--kind", "ValidatingWebhookConfiguration",
		"--db", dbPath,
		"--max-versions-per-object", "1",
	}, &search, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"kind": "ValidatingWebhookConfiguration"`,
		`"fact:admission_webhook.failure_policy"`,
		`"admission_webhook.service"`,
		`"versions": [`,
	} {
		if !strings.Contains(search.String(), want) {
			t.Fatalf("search missing %q: %s", want, search.String())
		}
	}
}

func TestRunQuerySchemaAndReadOnlySQLWithSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pod.json")
	dbPath := filepath.Join(dir, "kube-insight.db")
	err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "status": {"phase": "Running"}
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"ingest", "--file", path, "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	err = Run(context.Background(), []string{"query", "schema", "--db", dbPath}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"name": "latest_index"`,
		`"name": "latest_documents"`,
		`"name": "object_facts"`,
		`"relationships":`,
		`"recipes":`,
		`"name": "coverage_first"`,
		"All timestamps are Unix milliseconds",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("schema missing %q: %s", want, stdout.String())
		}
	}

	stdout.Reset()
	err = Run(context.Background(), []string{
		"query", "sql",
		"--db", dbPath,
		"--sql", "select ok.kind, li.namespace, li.name from latest_index li join object_kinds ok on ok.id = li.kind_id",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind": "Pod"`, `"namespace": "default"`, `"rowCount": 1`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("sql output missing %q: %s", want, stdout.String())
		}
	}

	stdout.Reset()
	err = Run(context.Background(), []string{
		"query", "sql",
		"--db", dbPath,
		"--sql", "delete from latest_index",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only rejection, got %v", err)
	}
}

func TestRunIngestDirWithSQLite(t *testing.T) {
	dir := t.TempDir()
	clusterDir := filepath.Join(dir, "cluster-a")
	if err := os.Mkdir(clusterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"not":"a resource"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := os.WriteFile(filepath.Join(clusterDir, "pods.json"), []byte(`{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [{
	    "apiVersion": "v1",
	    "kind": "Pod",
	    "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	    "spec": {"nodeName": "node-a"},
	    "status": {"phase": "Running"}
	  }]
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	dbPath := filepath.Join(dir, "kube-insight.db")
	err = Run(context.Background(), []string{"ingest", "--dir", dir, "--db", dbPath}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"StoredObservations": 1`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunGenerateSamples(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"generate", "samples",
		"--fixtures", filepath.Join("..", "..", "testdata", "fixtures", "kube"),
		"--output", dir,
		"--clusters", "1",
		"--copies", "2",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"clusters": 1`) {
		t.Fatalf("stdout = %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cluster-gen-01", "generated.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRunConfigValidate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"config", "validate",
		"--file", filepath.Join("..", "..", "config", "kube-insight.example.yaml"),
		"--output", "json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"ok": true`,
		`"role": "all"`,
		`"storage": "sqlite"`,
		`"collection": true`,
		`"resourcesAll": true`,
		`"filters":`,
		`"extractors":`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %s: %s", want, stdout.String())
		}
	}
}

func TestRunConfigValidateEnvAndFlagPrecedence(t *testing.T) {
	t.Setenv("KUBE_INSIGHT_STORAGE_SQLITE_PATH", "env.db")
	t.Setenv("KUBE_INSIGHT_COLLECTION_CONTEXTS", "staging,prod")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"config", "validate",
		"--file", filepath.Join("..", "..", "config", "kube-insight.example.yaml"),
		"--output", "json",
		"--db", "flag.db",
		"--namespace", "payments",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"sqlitePath": "flag.db"`,
		`"contexts": [`,
		`"staging"`,
		`"prod"`,
		`"namespace": "payments"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %s: %s", want, stdout.String())
		}
	}
}

func TestRunBenchmarkLocal(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "benchmark.db")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"benchmark", "local",
		"--fixtures", filepath.Join("..", "..", "testdata", "fixtures", "kube"),
		"--output", filepath.Join(dir, "samples"),
		"--db", dbPath,
		"--clusters", "1",
		"--copies", "1",
		"--query-runs", "3",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"dataset": "generated"`,
		`"objects":`,
		`"stored_versions_per_second":`,
		`"latest_lookup_p95_ms":`,
		`"query_runs": 3`,
		`"ingest":`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %s: %s", want, out)
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
}

func TestRunValidatePoC(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "poc.db")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"validate", "poc",
		"--fixtures", filepath.Join("..", "..", "testdata", "fixtures", "kube"),
		"--output", filepath.Join(dir, "samples"),
		"--db", dbPath,
		"--clusters", "1",
		"--copies", "2",
		"--query-runs", "2",
		"--max-stored-to-raw-ratio", "25",
		"--max-latest-lookup-p95-ms", "250",
		"--max-historical-get-p95-ms", "250",
		"--max-service-investigation-p95-ms", "5000",
		"--min-service-versions", "3",
		"--min-service-diffs", "1",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"passed": true`,
		`"status": "pass"`,
		`"summaryText": "PASS checks=`,
		`"checksFailed": 0`,
		`"name": "storage growth bounded"`,
		`"name": "latest lookup latency bounded"`,
		`"name": "historical get latency bounded"`,
		`"name": "service version diffs produced"`,
		`"service_investigation_diffs":`,
		`"versionDiffs":`,
		`"resourceQueue":`,
		`"priority": "high"`,
		`"name": "fake per-gvr queue metrics"`,
		`"name": "fake per-priority queue metrics"`,
		`"secret_payload_violations": 0`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %s: %s", want, out)
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
}

func TestRunBenchmarkWatchRequiresResourceOrDiscovery(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"benchmark", "watch",
		"--db", filepath.Join(t.TempDir(), "watch.db"),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires --resource or --discover-resources") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunWatchHelpShowsPositionalResources(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"watch", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"watch [RESOURCE_PATTERN ...]",
		"v1/*",
		"apps/v1/*",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "Available Commands") || strings.Contains(out, "watch resource") {
		t.Fatalf("legacy subcommands should be hidden: %s", out)
	}
}

func TestRunDBResourcesHealth(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID: "c1",
		Resource: kubeapi.ResourceInfo{
			Version:    "v1",
			Resource:   "pods",
			Kind:       "Pod",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
		ResourceVersion: "10",
		Event:           storage.OffsetEventWatch,
		Status:          "watch_error",
		Error:           "stream reset",
	})
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"db", "resources", "health",
		"--db", dbPath,
		"--errors-only",
		"--output", "json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"errors": 1`,
		`"status": "watch_error"`,
		`"error": "stream reset"`,
		`"resource": "pods"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestWatchResourcePatternMatching(t *testing.T) {
	resources := []collector.Resource{
		{Name: "pods", Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		{Name: "deployments.apps", Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Name: "deployments.apps", Group: "apps", Version: "v1beta1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Name: "leases.coordination.k8s.io", Group: "coordination.k8s.io", Version: "v1", Resource: "leases", Kind: "Lease", Namespaced: true},
	}
	matched := matchResourcePatterns(resources, []string{"v1/*", "apps/v1/*"})
	if len(matched) != 2 {
		t.Fatalf("matched = %#v", matched)
	}
	if matched[0].Name != "pods" || matched[1].Name != "deployments.apps" || matched[1].Version != "v1" {
		t.Fatalf("matched = %#v", matched)
	}
}

func TestDefaultWatchDBPathIsStable(t *testing.T) {
	got := defaultWatchDBPath("kind/dev:west")
	if got != "kubeinsight.db" {
		t.Fatalf("db path = %q", got)
	}
}

func TestCompareResourceCoverage(t *testing.T) {
	live := []collector.Resource{
		{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		{Name: "deployments.apps", Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
	}
	stored := []collector.Resource{
		{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		{Name: "jobs.batch", Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true},
	}

	report := compareResourceCoverage("dev", live, stored)
	if report.LiveResources != 2 || report.Matched != 1 {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Missing) != 1 || report.Missing[0].Name != "deployments.apps" {
		t.Fatalf("missing = %#v", report.Missing)
	}
	if len(report.Extra) != 1 || report.Extra[0].Name != "jobs.batch" {
		t.Fatalf("extra = %#v", report.Extra)
	}
}

func TestRunDBClustersDeleteRequiresConfirmation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--db", filepath.Join(t.TempDir(), "kubeinsight.db"),
		"db", "clusters", "delete", "k8s-test",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunCollectIngestRequiresDB(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"collect", "ingest", "--resource", "pods"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires --db") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunWriteCommandRejectsAPIRole(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--role", "api",
		"--collection-enabled=false",
		"collect", "ingest",
		"--db", filepath.Join(dir, "kube-insight.db"),
		"--resource", "pods",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "disabled when instance.role is api") {
		t.Fatalf("error = %v", err)
	}
}

func TestAPIInfosToResourcesFiltersAndFormats(t *testing.T) {
	resources := apiInfosToResources([]kubeapi.ResourceInfo{
		{Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
		{Group: "example.com", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true, Verbs: []string{"get", "list"}},
		{Group: "example.com", Version: "v1", Resource: "writeonlys", Kind: "WriteOnly", Namespaced: true, Verbs: []string{"create"}},
	})
	if len(resources) != 2 {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Name != "pods" {
		t.Fatalf("resource[0] = %#v", resources[0])
	}
	if resources[1].Name != "widgets.example.com" || resources[1].Group != "example.com" {
		t.Fatalf("resource[1] = %#v", resources[1])
	}
}

func TestMergeCollectorResourcesPrefersFirst(t *testing.T) {
	out := mergeCollectorResources(
		[]collector.Resource{{Name: "widgets.example.com", Group: "example.com", Resource: "widgets", Kind: "Widget", Namespaced: true}},
		[]collector.Resource{{Name: "widgets.example.com", Namespaced: true}},
	)
	if len(out) != 1 {
		t.Fatalf("resources = %#v", out)
	}
	if out[0].Kind != "Widget" || out[0].Group != "example.com" {
		t.Fatalf("resource = %#v", out[0])
	}
}

func TestEnrichCollectorResourcesKeepsExplicitList(t *testing.T) {
	out := enrichCollectorResources(
		[]collector.Resource{{Name: "pods", Namespaced: true}},
		[]collector.Resource{
			{Name: "pods", Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
			{Name: "widgets.example.com", Group: "example.com", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true},
		},
	)
	if len(out) != 1 {
		t.Fatalf("resources = %#v", out)
	}
	if out[0].Name != "pods" || out[0].Kind != "Pod" || out[0].Resource != "pods" {
		t.Fatalf("resource = %#v", out[0])
	}
}

func TestClusterIDForPath(t *testing.T) {
	root := filepath.Join("testdata", "kube-samples")
	path := filepath.Join(root, "cluster-a", "pods.json")
	if got := clusterIDForPath(root, path); got != "cluster-a" {
		t.Fatalf("clusterIDForPath = %q, want cluster-a", got)
	}

	root = filepath.Join("testdata", "kube-samples", "cluster-b")
	path = filepath.Join(root, "pods.json")
	if got := clusterIDForPath(root, path); got != "cluster-b" {
		t.Fatalf("clusterIDForPath = %q, want cluster-b", got)
	}
}
