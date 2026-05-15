package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyRetentionDeletesExpiredHistoryAndKeepsLatest(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	oldMillis := millis(now.Add(-48 * time.Hour))
	newMillis := millis(now)
	if _, err := store.db.ExecContext(ctx, `
insert into clusters(id, name, created_at) values (1, 'c1', ?);
insert into api_resources(id, api_group, api_version, resource, kind, namespaced, verbs) values (1, '', 'v1', 'pods', 'Pod', true, '["list","watch"]');
insert into object_kinds(id, api_resource_id, api_group, api_version, kind) values (1, 1, '', 'v1', 'Pod');
insert into objects(id, cluster_id, kind_id, namespace, name, uid, latest_version_id, first_seen_at, last_seen_at) values (1, 1, 1, 'default', 'pod-1', 'uid-1', 2, ?, ?);
insert into blobs(digest, codec, raw_size, stored_size, data) values ('old', 'identity', 2, 2, '{}'), ('new', 'identity', 2, 2, '{}');
insert into versions(id, object_id, seq, observed_at, resource_version, generation, doc_hash, materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary) values
  (1, 1, 1, ?, '10', null, 'old', 'full', 'full_identity', 'old', 2, 2, 0, '{}'),
  (2, 1, 2, ?, '11', null, 'new', 'full', 'full_identity', 'new', 2, 2, 0, '{}');
insert into latest_index(object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at) values (1, 1, 1, 'default', 'pod-1', 'uid-1', 2, ?);
insert into object_observations(cluster_id, object_id, observed_at, observation_type, resource_version, version_id, content_changed) values
  (1, 1, ?, 'ADDED', '10', 1, true),
  (1, 1, ?, 'MODIFIED', '11', 2, true),
  (1, 1, ?, 'MODIFIED', '12', null, false);
insert into object_facts(cluster_id, ts, object_id, version_id, kind_id, namespace, name, fact_key) values (1, ?, 1, 1, 1, 'default', 'pod-1', 'phase');
insert into object_changes(cluster_id, ts, object_id, version_id, change_family, path, op) values (1, ?, 1, 1, 'spec', '/spec/nodeName', 'replace');
insert into object_edges(cluster_id, edge_type, src_id, dst_id, valid_from, valid_to, src_version_id, dst_version_id) values (1, 'owner', 1, 1, ?, ?, 1, 1);
insert into filter_decisions(ts, cluster_name, api_group, api_version, resource, kind, namespace, name, observation_type, filter_name, outcome, reason, destructive) values
  (?, 'c1', '', 'v1', 'pods', 'Pod', 'default', 'pod-1', 'ADDED', 'managed_fields', 'keep_modified', 'managed_fields_removed', true),
  (?, 'c1', '', 'v1', 'pods', 'Pod', 'default', 'pod-1', 'MODIFIED', 'managed_fields', 'keep_modified', 'managed_fields_removed', true);
`, newMillis, oldMillis, newMillis, oldMillis, newMillis, newMillis, oldMillis, newMillis, oldMillis, oldMillis, oldMillis, oldMillis, oldMillis, oldMillis, newMillis); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
update versions set observed_at = case id when 1 then ? when 2 then ? else observed_at end;
update object_observations set observed_at = case version_id when 1 then ? when 2 then ? else ? end;
update object_facts set ts = ?;
update object_changes set ts = ?;
update object_edges set valid_from = ?, valid_to = ?;
`, oldMillis, newMillis, oldMillis, newMillis, oldMillis, oldMillis, oldMillis, oldMillis, oldMillis); err != nil {
		t.Fatal(err)
	}

	report, err := store.ApplyRetention(ctx, RetentionOptions{
		MaxAge:               24 * time.Hour,
		MinVersionsPerObject: 1,
		FilterDecisionMaxAge: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Versions != 1 || report.Observations != 2 || report.Facts != 1 || report.Changes != 1 || report.Edges != 1 || report.Blobs != 1 || report.FilterDecisions != 1 {
		t.Fatalf("report = %#v", report)
	}
	assertCount(t, store, "versions", 1)
	assertCount(t, store, "blobs", 1)
	assertCount(t, store, "object_observations", 1)
	assertCount(t, store, "object_facts", 0)
	assertCount(t, store, "object_changes", 0)
	assertCount(t, store, "object_edges", 0)
	assertCount(t, store, "filter_decisions", 1)
}

func TestApplyRetentionRulesTargetProfiles(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	oldMillis := millis(now.Add(-48 * time.Hour))
	newMillis := millis(now)
	if _, err := store.db.ExecContext(ctx, `
insert into clusters(id, name, created_at) values (1, 'c1', ?);
insert into api_resources(id, api_group, api_version, resource, kind, namespaced, verbs) values
  (1, 'events.k8s.io', 'v1', 'events', 'Event', true, '["list","watch"]'),
  (2, '', 'v1', 'pods', 'Pod', true, '["list","watch"]');
insert into resource_processing_profiles(api_resource_id, profile, retention_class, filter_chain, extractor_set, compaction_strategy, priority, max_event_buffer, enabled) values
  (1, 'event_rollup', 'churn', 'default', 'event', 'rollup', 'high', 256, true),
  (2, 'pod_fast_path', 'hot', 'default', 'pod', 'status_aware', 'high', 256, true);
insert into object_kinds(id, api_resource_id, api_group, api_version, kind) values
  (1, 1, 'events.k8s.io', 'v1', 'Event'),
  (2, 2, '', 'v1', 'Pod');
insert into objects(id, cluster_id, kind_id, namespace, name, uid, latest_version_id, first_seen_at, last_seen_at) values
  (1, 1, 1, 'default', 'event-1', 'event-uid', 2, ?, ?),
  (2, 1, 2, 'default', 'pod-1', 'pod-uid', 4, ?, ?);
insert into blobs(digest, codec, raw_size, stored_size, data) values
  ('event-old', 'identity', 2, 2, '{}'), ('event-new', 'identity', 2, 2, '{}'),
  ('pod-old', 'identity', 2, 2, '{}'), ('pod-new', 'identity', 2, 2, '{}');
insert into versions(id, object_id, seq, observed_at, resource_version, generation, doc_hash, materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary) values
  (1, 1, 1, ?, '10', null, 'event-old', 'full', 'full_identity', 'event-old', 2, 2, 0, '{}'),
  (2, 1, 2, ?, '11', null, 'event-new', 'full', 'full_identity', 'event-new', 2, 2, 0, '{}'),
  (3, 2, 1, ?, '20', null, 'pod-old', 'full', 'full_identity', 'pod-old', 2, 2, 0, '{}'),
  (4, 2, 2, ?, '21', null, 'pod-new', 'full', 'full_identity', 'pod-new', 2, 2, 0, '{}');
insert into latest_index(object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at) values
  (1, 1, 1, 'default', 'event-1', 'event-uid', 2, ?),
  (2, 1, 2, 'default', 'pod-1', 'pod-uid', 4, ?);
`, newMillis, oldMillis, newMillis, oldMillis, newMillis, oldMillis, newMillis, oldMillis, newMillis, newMillis, newMillis); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
update objects set latest_version_id = case id when 1 then 2 when 2 then 4 else latest_version_id end;
update versions set observed_at = case id when 1 then ? when 2 then ? when 3 then ? when 4 then ? else observed_at end;
update latest_index set latest_version_id = case object_id when 1 then 2 when 2 then 4 else latest_version_id end,
  observed_at = ?;
`, oldMillis, newMillis, oldMillis, newMillis, newMillis); err != nil {
		t.Fatal(err)
	}

	report, err := store.ApplyRetention(ctx, RetentionOptions{
		MinVersionsPerObject: 1,
		Rules: []RetentionRuleOptions{{
			Name:     "events",
			Profiles: []string{"event_rollup"},
			MaxAge:   24 * time.Hour,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Versions != 1 || len(report.Rules) != 1 || report.Rules[0].Versions != 1 {
		t.Fatalf("report = %#v", report)
	}
	assertCount(t, store, "versions", 3)
	assertCount(t, store, "blobs", 3)
}

func TestRetentionRuleFilterSQLSupportsResourceGlob(t *testing.T) {
	filterSQL, args := retentionRuleFilterSQL(RetentionRuleOptions{Resources: []string{"*.reports.kyverno.io"}})
	if filterSQL == "" || len(args) != 2 {
		t.Fatalf("filterSQL=%q args=%#v", filterSQL, args)
	}
	if filterSQL != "(ranked.resource_name glob ? or ranked.gvr glob ?)" {
		t.Fatalf("filterSQL = %q", filterSQL)
	}
}

func assertCount(t *testing.T, store *Store, table string, want int64) {
	t.Helper()
	var got int64
	if err := store.db.QueryRow("select count(*) from " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
