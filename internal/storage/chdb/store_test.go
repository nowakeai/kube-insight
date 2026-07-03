//go:build !chdb

package chdb

import (
	"errors"
	"strings"
	"testing"
)

func TestNewStoreReturnsUnavailableUntilAdapterIsLinked(t *testing.T) {
	_, err := NewStore(Options{Path: "kubeinsight.chdb", Database: "kube_insight"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "chdb-go") {
		t.Fatalf("err should mention chdb-go setup, got %v", err)
	}
}

func TestNewStoreValidatesOptions(t *testing.T) {
	if _, err := NewStore(Options{Database: "kube_insight"}); err == nil {
		t.Fatal("expected path error")
	}
	if _, err := NewStore(Options{Path: "kubeinsight.chdb"}); err == nil {
		t.Fatal("expected database error")
	}
	if _, err := NewStore(Options{Path: "kubeinsight.chdb", Database: "bad-name"}); err == nil {
		t.Fatal("expected database identifier error")
	}
	if _, err := NewStore(Options{Path: "kubeinsight.chdb", Database: "kube_insight", MaxSessions: -1}); err == nil {
		t.Fatal("expected maxSessions error")
	}
}
