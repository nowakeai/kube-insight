package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuerySchemaRecipesKeepClusterScope(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	schema, err := store.QuerySchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Recipes) == 0 {
		t.Fatal("expected schema recipes")
	}
	for _, recipe := range schema.Recipes {
		if recipe.Name == "cluster_scope" {
			continue
		}
		sql := strings.ToLower(recipe.SQL)
		if !strings.Contains(sql, "cluster_id = 1") && !strings.Contains(sql, "c.id = 1") {
			t.Fatalf("recipe %q is not cluster-scoped:\n%s", recipe.Name, recipe.SQL)
		}
	}
}

func TestQuerySchemaEventRecipesAvoidPreviewExactMatchIndex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	schema, err := store.QuerySchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range schema.Recipes {
		if recipe.Name != "warning_event_reason_counts" && recipe.Name != "policy_violation_events" && recipe.Name != "event_to_involved_object" {
			continue
		}
		sql := strings.ToLower(recipe.SQL)
		if !strings.Contains(sql, "fact_key <> 'k8s_event.message_preview'") {
			t.Fatalf("event recipe %q can hit high-cardinality preview index values:\n%s", recipe.Name, recipe.SQL)
		}
	}
}
