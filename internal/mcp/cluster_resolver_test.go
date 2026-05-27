package mcp

import (
	"context"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

type clusterResolverStore struct {
	clusters []storage.ClusterRecord
}

func (clusterResolverStore) Close() error { return nil }

func (s clusterResolverStore) ListClusters(context.Context) ([]storage.ClusterRecord, error) {
	return s.clusters, nil
}

func TestResolveClusterIDMatchesDisplaySourceAndUID(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{{
		Name:   "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
		UID:    "ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
		Source: "demo-project_us-central1_demo-cluster-2 https://192.0.2.10",
	}}}
	for _, input := range []string{
		"k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
		"ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
		"demo-project_us-central1_demo-cluster-2",
		"demo-project_us-central1_demo-cluster-2 https://192.0.2.10",
	} {
		got, err := resolveClusterID(context.Background(), store, input)
		if err != nil {
			t.Fatalf("resolveClusterID(%q) error = %v", input, err)
		}
		if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
			t.Fatalf("resolveClusterID(%q) = %q", input, got)
		}
	}
}

func TestResolveClusterIDMatchesAbbreviatedClusterFragments(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{{
		Name:   "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
		Source: "demo-project_us-central1_demo-cluster-2 https://192.0.2.10",
	}}}
	for _, input := range []string{"gcp2", "gcp cluster 2", "cluster2", "gke2"} {
		got, err := resolveClusterID(context.Background(), store, input)
		if err != nil {
			t.Fatalf("resolveClusterID(%q) error = %v", input, err)
		}
		if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
			t.Fatalf("resolveClusterID(%q) = %q", input, got)
		}
	}
}

func TestResolveClusterIDRejectsAmbiguousAbbreviations(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{
		{Name: "c1", Source: "demo-project_us-west1_demo-cluster-2"},
		{Name: "c2", Source: "demo-project_us-east1_demo-cluster-2"},
	}}
	_, err := resolveClusterID(context.Background(), store, "gcp2")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("resolveClusterID ambiguous error = %v", err)
	}
}

func TestResolveClusterIDRejectsUnknownWhenClustersKnown(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{{Name: "c1", Source: "prod"}}}
	_, err := resolveClusterID(context.Background(), store, "missing")
	if err == nil || !strings.Contains(err.Error(), "available clusters") {
		t.Fatalf("resolveClusterID unknown error = %v", err)
	}
}

func TestResolveClusterIDPassesThroughWhenClusterListingUnavailable(t *testing.T) {
	got, err := resolveClusterID(context.Background(), passThroughReadStore{}, "c1")
	if err != nil {
		t.Fatalf("resolveClusterID passthrough error = %v", err)
	}
	if got != "c1" {
		t.Fatalf("resolveClusterID passthrough = %q", got)
	}
}

type passThroughReadStore struct{}

func (passThroughReadStore) Close() error { return nil }
