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
		Latest: 23,
	}}}
	for _, input := range []string{"demo2", "demo cluster 2", "cluster2"} {
		got, err := resolveClusterID(context.Background(), store, input)
		if err != nil {
			t.Fatalf("resolveClusterID(%q) error = %v", input, err)
		}
		if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
			t.Fatalf("resolveClusterID(%q) = %q", input, got)
		}
	}
}

func TestResolveClusterIDPrefersDatafulFuzzyMatchOverEmptyExactAlias(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{
		{Name: "demo2"},
		{
			Name:   "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc",
			Source: "demo-project_us-central1_demo-cluster-2 https://192.0.2.10",
			Latest: 23,
		},
	}}
	got, err := resolveClusterID(context.Background(), store, "demo2")
	if err != nil {
		t.Fatalf("resolveClusterID(demo2) error = %v", err)
	}
	if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
		t.Fatalf("resolveClusterID(demo2) = %q", got)
	}
}

func TestResolveClusterIDRejectsAmbiguousAbbreviations(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{
		{Name: "c1", Source: "demo-project_us-west1_demo-cluster-2"},
		{Name: "c2", Source: "demo-project_us-east1_demo-cluster-2"},
	}}
	_, err := resolveClusterID(context.Background(), store, "demo2")
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

func TestResolveClusterIDIgnoresBlankClusterMetadata(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{
		{Name: "", Source: "demo-project_us-central1_demo-cluster-2 https://192.0.2.10"},
		{Name: "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc", Latest: 23},
	}}
	got, err := resolveClusterID(context.Background(), store, "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc")
	if err != nil {
		t.Fatalf("resolveClusterID stable id error = %v", err)
	}
	if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
		t.Fatalf("resolveClusterID stable id = %q", got)
	}
	_, err = resolveClusterID(context.Background(), store, "missing")
	if err == nil || strings.Contains(err.Error(), "id=") {
		t.Fatalf("resolveClusterID unknown error = %v", err)
	}
}

func TestResolveClusterIDPassesThroughWhenOnlyBlankClusterMetadataExists(t *testing.T) {
	store := clusterResolverStore{clusters: []storage.ClusterRecord{
		{Name: "", Source: "demo-project_us-central1_demo-cluster-2 https://192.0.2.10"},
		{Name: "   ", Source: "empty"},
	}}
	got, err := resolveClusterID(context.Background(), store, "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc")
	if err != nil {
		t.Fatalf("resolveClusterID blank metadata passthrough error = %v", err)
	}
	if got != "k8s-ac26a5e8-69aa-400e-9f3d-ef14dbd9cadc" {
		t.Fatalf("resolveClusterID blank metadata passthrough = %q", got)
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
