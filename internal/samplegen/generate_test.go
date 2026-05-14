package samplegen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSamples(t *testing.T) {
	out := filepath.Join(t.TempDir(), "samples")
	manifest, err := Generate(context.Background(), Options{
		FixturesDir: filepath.Join("..", "..", "testdata", "fixtures", "kube"),
		OutputDir:   out,
		Clusters:    2,
		Copies:      3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(manifest.Entries))
	}
	if manifest.Entries[0].Items == 0 {
		t.Fatal("generated no items")
	}
	if _, err := os.Stat(filepath.Join(out, "cluster-gen-01", "generated.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "cluster-gen-01", "generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	objects, err := decodeObjects(data)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGeneratedMultiVersionPod(objects) {
		t.Fatal("generated samples missing multi-version pod scenario")
	}
}

func hasGeneratedMultiVersionPod(objects []map[string]any) bool {
	seen := map[string]int{}
	for _, obj := range objects {
		if kind, _ := obj["kind"].(string); kind != "Pod" {
			continue
		}
		metadata, _ := obj["metadata"].(map[string]any)
		uid, _ := metadata["uid"].(string)
		if uid != "" {
			seen[uid]++
		}
	}
	for _, count := range seen {
		if count > 1 {
			return true
		}
	}
	return false
}
