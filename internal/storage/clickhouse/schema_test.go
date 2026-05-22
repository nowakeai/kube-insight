package clickhouse

import (
	"strings"
	"testing"
)

func TestCreateTableStatementsDefaultStringPayload(t *testing.T) {
	stmts, err := CreateTableStatements(SchemaOptions{
		Database:         "kube_insight",
		ColdVolume:       "cold",
		ColdAfterSeconds: 604800,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"CREATE DATABASE IF NOT EXISTS `kube_insight`",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.api_resources",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.observations",
		"doc String CODEC(ZSTD(3))",
		"ENGINE = MergeTree",
		"ORDER BY (cluster_id, kind, namespace, name, observed_at, uid)",
		"TTL toDateTime(observed_at) + INTERVAL 604800 SECOND TO VOLUME 'cold'",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.object_aliases",
		"ENGINE = ReplacingMergeTree(observed_at)",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.facts",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.edges",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.filter_decisions",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.ingestion_offsets",
		"ENGINE = ReplacingMergeTree(updated_at)",
		"ORDER BY (cluster_id, api_group, api_version, resource, namespace, event)",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.agent_sessions",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.agent_runs",
		"CREATE TABLE IF NOT EXISTS `kube_insight`.agent_run_events",
		"ALTER TABLE `kube_insight`.`versions` ADD INDEX IF NOT EXISTS `versions_object_id_bf` `object_id` TYPE bloom_filter(0.01) GRANULARITY 4",
		"ALTER TABLE `kube_insight`.`edges` ADD INDEX IF NOT EXISTS `edges_dst_id_bf` `dst_id` TYPE bloom_filter(0.01) GRANULARITY 4",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("schema missing %q:\n%s", want, joined)
		}
	}
}

func TestCreateTableStatementsJSONPayloadAndCluster(t *testing.T) {
	stmts, err := CreateTableStatements(SchemaOptions{
		Database:    "ki",
		Cluster:     "prod_cluster",
		UseJSONType: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		"ON CLUSTER `prod_cluster`",
		"doc JSON",
		"ALTER TABLE `ki`.`facts` ON CLUSTER `prod_cluster` ADD INDEX IF NOT EXISTS `facts_object_id_bf`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("schema missing %q:\n%s", want, joined)
		}
	}
}

func TestCreateTableStatementsRejectsBadIdentifier(t *testing.T) {
	_, err := CreateTableStatements(SchemaOptions{Database: "bad-name"})
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("err = %v", err)
	}
}

func TestOptionsFromConfigCarriesSchemaOptions(t *testing.T) {
	opts := Options{
		DSNEnv:           "KUBE_INSIGHT_CLICKHOUSE_DSN",
		Database:         "ki",
		Cluster:          "prod_cluster",
		ColdVolume:       "cold",
		ColdAfterSeconds: 3600,
		UseJSONType:      true,
		InitOnStart:      true,
		AsyncInsert:      true,
		BatchSize:        1000,
		FlushIntervalMS:  1000,
	}
	schema := opts.SchemaOptions()
	if !opts.InitOnStart {
		t.Fatalf("options = %#v", opts)
	}
	if schema.Database != "ki" || schema.Cluster != "prod_cluster" || !schema.UseJSONType || schema.ColdAfterSeconds != 3600 {
		t.Fatalf("schema options = %#v", schema)
	}
}

func TestSQLHelpersEscapeDynamicValues(t *testing.T) {
	if got := quoteString("default'; DROP TABLE versions; --"); got != "'default''; DROP TABLE versions; --'" {
		t.Fatalf("quoteString = %s", got)
	}
	if got := q("ki`prod"); got != "`ki``prod`" {
		t.Fatalf("q = %s", got)
	}
	if got := sqlStringList([]string{"a'b", "c"}); got != "'a''b','c'" {
		t.Fatalf("sqlStringList = %s", got)
	}
}

func TestBoundedLimit(t *testing.T) {
	if got := boundedLimit(-1, 50, 1000); got != 50 {
		t.Fatalf("negative limit = %d", got)
	}
	if got := boundedLimit(2000, 50, 1000); got != 1000 {
		t.Fatalf("capped limit = %d", got)
	}
	if got := boundedLimit(25, 50, 1000); got != 25 {
		t.Fatalf("normal limit = %d", got)
	}
}
