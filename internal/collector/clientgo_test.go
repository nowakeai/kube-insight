package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestCurrentContextClientGoFallsBackToInCluster(t *testing.T) {
	withFakeInClusterConfig(t, &rest.Config{Host: "https://kubernetes.default.svc"})
	withEmptyKubeconfig(t)

	got, err := CurrentContextClientGo()
	if err != nil {
		t.Fatalf("CurrentContextClientGo() error = %v", err)
	}
	if got != InClusterContextName {
		t.Fatalf("CurrentContextClientGo() = %q, want %q", got, InClusterContextName)
	}
}

func TestRestConfigWithTimeoutUsesInClusterConfig(t *testing.T) {
	withFakeInClusterConfig(t, &rest.Config{Host: "https://kubernetes.default.svc"})

	got, err := restConfigWithTimeout(InClusterContextName, 7*time.Second)
	if err != nil {
		t.Fatalf("restConfigWithTimeout() error = %v", err)
	}
	if got.Host != "https://kubernetes.default.svc" {
		t.Fatalf("Host = %q", got.Host)
	}
	if got.UserAgent != "kube-insight" || got.QPS != 20 || got.Burst != 40 || got.Timeout != 7*time.Second {
		t.Fatalf("REST config defaults were not applied: %#v", got)
	}
}

func withFakeInClusterConfig(t *testing.T, cfg *rest.Config) {
	t.Helper()
	original := inClusterConfig
	inClusterConfig = func() (*rest.Config, error) {
		if cfg == nil {
			return nil, fmt.Errorf("not in cluster")
		}
		copy := *cfg
		return &copy, nil
	}
	t.Cleanup(func() {
		inClusterConfig = original
	})
}

func withEmptyKubeconfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	data := []byte("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", path)
}
