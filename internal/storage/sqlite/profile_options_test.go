package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
)

func TestOpenWithOptionsUsesProfileRules(t *testing.T) {
	store, err := OpenWithOptions(filepath.Join(t.TempDir(), "kube-insight.db"), Options{
		ProfileRules: []resourceprofile.Rule{
			{
				Name:      "custom_event",
				Resources: []string{"events.events.k8s.io"},
				Profile:   resourceprofile.Profile{RetentionPolicy: "short", ExtractorSet: "event", Priority: "low"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertAPIResources(context.Background(), []kubeapi.ResourceInfo{
		{Group: "events.k8s.io", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	profiles, err := store.ResourceProcessingProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if profiles["custom_event"] != 1 {
		t.Fatalf("profiles = %#v", profiles)
	}
}
