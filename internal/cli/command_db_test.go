package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func writeClickHouseTSV(w http.ResponseWriter, header string, rows ...string) {
	w.Header().Set("Content-Type", "text/tab-separated-values")
	_, _ = w.Write([]byte(header + "\n"))
	for _, row := range rows {
		_, _ = w.Write([]byte(row + "\n"))
	}
}

func TestRunDBClickHouseSchemaSQL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "schema",
		"--database", "ki",
		"--cold-volume", "cold",
		"--cold-after", "168h",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"CREATE DATABASE IF NOT EXISTS `ki`;",
		"CREATE TABLE IF NOT EXISTS `ki`.observations",
		"doc String CODEC(ZSTD(3))",
		"TTL toDateTime(observed_at) + INTERVAL 604800 SECOND TO VOLUME 'cold'",
		"CREATE TABLE IF NOT EXISTS `ki`.facts",
		"CREATE TABLE IF NOT EXISTS `ki`.edges",
		"ALTER TABLE `ki`.`versions` ADD INDEX IF NOT EXISTS `versions_object_id_bf`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
}

func TestRunDBClickHouseHelpGroupsMaintenanceCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"db", "clickhouse", "--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "maintenance") {
		t.Fatalf("clickhouse help missing maintenance group: %s", out)
	}
	for _, hidden := range []string{"backfill-service-facts", "cleanup-repair-artifacts", "repair-edge-kinds", "repair-ingestion-offsets"} {
		if strings.Contains(out, "  "+hidden+" ") {
			t.Fatalf("clickhouse help exposed maintenance command %q: %s", hidden, out)
		}
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"db", "clickhouse", "maintenance", "--help"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	for _, want := range []string{"backfill-service-facts", "cleanup-repair-artifacts", "repair-edge-kinds", "repair-ingestion-offsets"} {
		if !strings.Contains(out, want) {
			t.Fatalf("maintenance help missing %q: %s", want, out)
		}
	}
}

func TestRunDBClickHouseSchemaJSONTypeOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "schema",
		"--database", "ki",
		"--cluster", "prod_cluster",
		"--json-type",
		"--output", "json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"database": "ki"`,
		`"cluster": "prod_cluster"`,
		`"useJsonType": true`,
		"ON CLUSTER `prod_cluster`",
		"doc JSON",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
}

func TestRunDBClickHouseInit(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "init",
		"--endpoint", server.URL + "?user=ki&password=secret",
		"--database", "ki",
		"--cold-after", "0",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 18 {
		t.Fatalf("requests = %d %#v", len(requests), requests)
	}
	for _, want := range []string{
		"database",
		"ki",
		"statements",
		"applied",
		"18",
		"password=***",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "secret") {
		t.Fatalf("stdout leaked endpoint password: %s", stdout.String())
	}
	if !strings.Contains(requests[0], "CREATE DATABASE IF NOT EXISTS `ki`") {
		t.Fatalf("first request = %s", requests[0])
	}
	if !strings.Contains(strings.Join(requests, "\n"), "CREATE TABLE IF NOT EXISTS `ki`.filter_decisions") {
		t.Fatalf("requests missing filter_decisions table: %#v", requests)
	}
	if !strings.Contains(requests[len(requests)-1], "ALTER TABLE `ki`.`edges` ADD INDEX IF NOT EXISTS `edges_dst_id_bf`") {
		t.Fatalf("last request = %s", requests[len(requests)-1])
	}
}

func TestRunDBClickHouseStatusReportsDrift(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query = string(data)
		_, _ = w.Write([]byte(`{"data":[{"name":"api_resources","engine":"ReplacingMergeTree","sorting_key":"api_group, api_version, resource"},{"name":"ingestion_offsets","engine":"MergeTree","sorting_key":"cluster_id, api_group, api_version, resource, namespace, updated_at"}],"rows":2}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "status",
		"--endpoint", server.URL + "?user=ki&password=secret",
		"--database", "ki",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"endpoint", "password=***", "ok", "no", "ingestion_offsets", "ReplacingMergeTree", "engine=MergeTree want=ReplacingMergeTree"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("stdout leaked endpoint password: %s", out)
	}
	if !strings.Contains(query, "FROM system.tables") || !strings.Contains(query, "'ingestion_offsets'") {
		t.Fatalf("query = %s", query)
	}
}

func TestRunDBClickHouseStatusStrictFailsOnDrift(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"data":[{"name":"ingestion_offsets","engine":"MergeTree","sorting_key":"cluster_id, api_group, api_version, resource, namespace, updated_at"}],"rows":1}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "status",
		"--endpoint", server.URL,
		"--database", "ki",
		"--strict",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "schema status has drift") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDBClickHouseRepairIngestionOffsetsDryRun(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		_, _ = w.Write([]byte(`{"data":[{"name":"api_resources","engine":"ReplacingMergeTree","sorting_key":"api_group, api_version, resource"},{"name":"ingestion_offsets","engine":"MergeTree","sorting_key":"cluster_id, api_group, api_version, resource, namespace, updated_at"}],"rows":2}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "repair-ingestion-offsets",
		"--endpoint", server.URL + "?user=ki&password=secret",
		"--database", "ki",
		"--suffix", "20260516_130000",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || !strings.Contains(requests[0], "FROM system.tables") {
		t.Fatalf("requests = %#v", requests)
	}
	out := stdout.String()
	for _, want := range []string{"password=***", "needed", "yes", "dryRun", "ingestion_offsets_repair_20260516_130000", "argMax(status, updated_at)", "RENAME TABLE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("stdout leaked endpoint password: %s", out)
	}
}

func TestRunDBClickHouseRepairIngestionOffsetsApplyRequiresYes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"data":[{"name":"ingestion_offsets","engine":"MergeTree","sorting_key":"cluster_id, api_group, api_version, resource, namespace, updated_at"}],"rows":1}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "repair-ingestion-offsets",
		"--endpoint", server.URL,
		"--database", "ki",
		"--suffix", "20260516_130000",
		"--apply",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDBClickHouseRepairIngestionOffsetsApply(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		requests = append(requests, query)
		if strings.Contains(query, "FROM system.tables") {
			_, _ = w.Write([]byte(`{"data":[{"name":"ingestion_offsets","engine":"MergeTree","sorting_key":"cluster_id, api_group, api_version, resource, namespace, updated_at"}],"rows":1}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "repair-ingestion-offsets",
		"--endpoint", server.URL,
		"--database", "ki",
		"--suffix", "20260516_130000",
		"--apply",
		"--yes",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 {
		t.Fatalf("requests = %#v", requests)
	}
	if !strings.Contains(requests[1], "CREATE TABLE IF NOT EXISTS `ki`.`ingestion_offsets_repair_20260516_130000`") || !strings.Contains(requests[3], "RENAME TABLE") {
		t.Fatalf("requests = %#v", requests)
	}
	if !strings.Contains(stdout.String(), "applied") || !strings.Contains(stdout.String(), "3") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunDBClickHouseCleanupRepairArtifactsDryRun(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		_, _ = w.Write([]byte(`{"data":[{"name":"ingestion_offsets_backup_20260516_195840","engine":"MergeTree","rows":"59015","bytes":"1292433"},{"name":"ingestion_offsets_repair_20260516_195639","engine":"ReplacingMergeTree","rows":"0","bytes":"0"},{"name":"ingestion_offsets_repair_20260516_200000","engine":"ReplacingMergeTree","rows":"10","bytes":"99"}],"rows":3}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "cleanup-repair-artifacts",
		"--endpoint", server.URL + "?user=ki&password=secret",
		"--database", "ki",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || !strings.Contains(requests[0], "FROM system.tables") {
		t.Fatalf("requests = %#v", requests)
	}
	out := stdout.String()
	for _, want := range []string{"password=***", "dryRun", "yes", "droppable", "1", "ingestion_offsets_backup_20260516_195840", "ingestion_offsets_repair_20260516_195639", "drop-empty-repair"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("stdout leaked endpoint password: %s", out)
	}
}

func TestRunDBClickHouseCleanupRepairArtifactsYesDropsOnlyEmptyRepair(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		requests = append(requests, query)
		if strings.Contains(query, "FROM system.tables") {
			_, _ = w.Write([]byte(`{"data":[{"name":"ingestion_offsets_backup_20260516_195840","engine":"MergeTree","rows":"59015","bytes":"1292433"},{"name":"ingestion_offsets_repair_20260516_195639","engine":"ReplacingMergeTree","rows":"0","bytes":"0"},{"name":"ingestion_offsets_repair_20260516_200000","engine":"ReplacingMergeTree","rows":"10","bytes":"99"}],"rows":3}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "cleanup-repair-artifacts",
		"--endpoint", server.URL,
		"--database", "ki",
		"--yes",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v", requests)
	}
	if !strings.Contains(requests[1], "DROP TABLE IF EXISTS `ki`.`ingestion_offsets_repair_20260516_195639`") {
		t.Fatalf("drop request = %s", requests[1])
	}
	if strings.Contains(requests[1], "backup") || strings.Contains(requests[1], "200000") {
		t.Fatalf("drop request should not target backup or non-empty repair: %s", requests[1])
	}
	if !strings.Contains(stdout.String(), "applied") || !strings.Contains(stdout.String(), "1") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunDBClickHouseImportFile(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "pod.json")
	if err := os.WriteFile(path, []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "spec": {"nodeName": "node-a"},
	  "status": {"phase": "Running"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "import",
		"--endpoint", server.URL,
		"--database", "ki",
		"--file", path,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) < 3 {
		t.Fatalf("requests = %#v", requests)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{
		"INSERT INTO `ki`.`observations` FORMAT JSONEachRow",
		"INSERT INTO `ki`.`object_aliases` FORMAT JSONEachRow",
		"INSERT INTO `ki`.`versions` FORMAT JSONEachRow",
		"INSERT INTO `ki`.`facts` FORMAT JSONEachRow",
		`"name":"api-1"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %q: %s", want, joined)
		}
	}
	for _, want := range []string{"rows", "table.observations", "table.object_aliases", "table.versions", "table.facts"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunDBClickHouseService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id") && strings.Contains(query, "argMax"):
				writeClickHouseTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `endpointslice_for_service	c1/eps-uid	c1/svc-uid	EndpointSlice	Service	2026-05-17T00:00:00Z	`, `endpointslice_targets_pod	c1/eps-uid	c1/pod-uid	EndpointSlice	Pod	2026-05-17T00:00:00Z	`)
			case strings.Contains(query, "SELECT object_id, doc"):
				writeClickHouseTSV(w, "object_id	doc", `c1/svc-uid	{"kind":"Service"}`, `c1/eps-uid	{"kind":"EndpointSlice"}`, `c1/pod-uid	{"kind":"Pod"}`)
			case strings.Contains(query, "SELECT object_id, alias_id"):
				writeClickHouseTSV(w, "object_id	alias_id", `c1/svc-uid	c1/services/default/api`, `c1/eps-uid	c1/endpointslices/default/api-abc`, `c1/pod-uid	c1/pods/default/api-0`)
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id, valid_from"):
				writeClickHouseTSV(w, "edge_type	src_id	dst_id	valid_from	valid_to	detail", `endpointslice_targets_pod	c1/eps-uid	c1/pod-uid	2026-05-17T00:00:00Z		{}`)
			case strings.Contains(query, "SELECT object_id, seq"):
				writeClickHouseTSV(w, "object_id	seq	observed_at	resource_version	doc_hash	materialization	raw_size	stored_size", `c1/svc-uid	1	2026-05-17T00:00:00Z	1	abc	full	10	8`, `c1/eps-uid	1	2026-05-17T00:00:00Z	1	abc	full	10	8`, `c1/pod-uid	1	2026-05-17T00:00:00Z	1	abc	full	10	8`)
			case strings.Contains(query, "FROM `ki`.facts"):
				writeClickHouseTSV(w, "ts	object_id	fact_key	fact_value	numeric_value	severity	detail", `2026-05-17T00:00:00Z	c1/pod-uid	pod.phase	Running		10	{}`)
			case strings.Contains(query, "FROM `ki`.changes"):
				writeClickHouseTSV(w, "ts	object_id	change_family	path	op	old_scalar	new_scalar	severity")
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "kind = 'Service'"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","object_id":"c1/svc-uid","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"2026-05-17T00:00:00Z"}],"rows":1}`))
		case strings.Contains(query, "SELECT doc FROM `ki`.versions"):
			_, _ = w.Write([]byte(`{"data":[{"doc":"{\"kind\":\"Service\"}"}],"rows":1}`))
		case strings.Contains(query, "SELECT seq, observed_at"):
			_, _ = w.Write([]byte(`{"data":[{"seq":1,"observed_at":"2026-05-17T00:00:00Z","resource_version":"1","doc_hash":"abc","materialization":"full","raw_size":10,"stored_size":8}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.object_aliases") && strings.Contains(query, "WHERE object_id"):
			_, _ = w.Write([]byte(`{"data":[{"alias_id":"c1/services/default/api"},{"alias_id":"c1/endpointslices/default/api-abc"},{"alias_id":"c1/pods/default/api-0"}],"rows":3}`))
		case strings.Contains(query, "FROM `ki`.object_aliases"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/svc-uid","alias_id":"c1/services/default/api"},{"object_id":"c1/eps-uid","alias_id":"c1/endpointslices/default/api-abc"},{"object_id":"c1/pod-uid","alias_id":"c1/pods/default/api-0"}],"rows":3}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "argMax"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid","src_kind":"EndpointSlice","dst_kind":"Service","edge_valid_from":"2026-05-17T00:00:00Z"},{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","src_kind":"EndpointSlice","dst_kind":"Pod","edge_valid_from":"2026-05-17T00:00:00Z"}],"rows":2}`))
		case strings.Contains(query, "FROM `ki`.edges"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","valid_from":"2026-05-17T00:00:00Z"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/svc-uid","cluster_id":"c1","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"2026-05-17T00:00:00Z"},{"object_id":"c1/eps-uid","cluster_id":"c1","api_group":"discovery.k8s.io","api_version":"v1","resource":"endpointslices","kind":"EndpointSlice","namespace":"default","name":"api-abc","uid":"eps-uid","latest_observed_at":"2026-05-17T00:00:00Z"},{"object_id":"c1/pod-uid","cluster_id":"c1","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"2026-05-17T00:00:00Z"}],"rows":3}`))
		case strings.Contains(query, "FROM `ki`.facts"):
			_, _ = w.Write([]byte(`{"data":[{"ts":"2026-05-17T00:00:00Z","object_id":"c1/pod-uid","fact_key":"pod.phase","fact_value":"Running","severity":10}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.changes"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"db", "clickhouse", "service", "default", "api",
		"--endpoint", server.URL,
		"--database", "ki",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"kind": "Service"`,
		`"Key": "pod.phase"`,
		`"endpointSlices": 1`,
		`"pods": 1`,
		`"facts":`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunDBResourcesHealthTableOutput(t *testing.T) {
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
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Resource health summary",
		"Resources",
		"watch_error",
		"stream reset",
		"v1/pods",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunDBResourcesHealthClickHouseDriver(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		switch {
		case strings.Contains(query, "FROM `ki`.api_resources"):
			_, _ = w.Write([]byte(`{"data":[{"api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespaced":true,"verbs":"[\"list\",\"watch\"]"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.observations"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","namespace":"default","latest_objects":3}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.ingestion_offsets"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespaced":true,"namespace":"default","status":"watch_error","error":"stream reset","resource_version":"10","last_list_ms":0,"last_watch_ms":30000,"last_bookmark_ms":0,"updated_ms":30000}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: v1alpha1
storage:
  driver: clickhouse
  clickhouse:
    initOnStart: false
    dsnEnv: KUBE_INSIGHT_CLICKHOUSE_DSN
    database: ki
collection:
  enabled: false
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBE_INSIGHT_CLICKHOUSE_DSN", server.URL)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--config", configPath,
		"db", "resources", "health",
		"--cluster", "c1",
		"--errors-only",
		"--output", "json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"errors": 1`, `"status": "watch_error"`, `"latestObjects": 3`, `"stream reset"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
	if len(queries) != 3 {
		t.Fatalf("queries = %#v", queries)
	}
}

func TestRunDBReindexDryRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-1", UID: "pod-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "api-1", "namespace": "default", "uid": "pod-uid"},
		"status":     map[string]any{"phase": "Running"},
	}
	err = store.PutObservation(context.Background(), core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "10",
		Ref:             ref,
		Object:          obj,
	}, extractor.Evidence{})
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"db", "reindex",
		"--db", dbPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Evidence reindex",
		"dry-run",
		"Objects scanned",
		"Versions scanned",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}
