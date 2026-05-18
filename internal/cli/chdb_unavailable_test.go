//go:build !chdb

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunQuerySchemaWithChDBConfigExplainsNormalBuildUnavailable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"--config", "../../config/kube-insight.chdb.example.yaml",
		"query", "schema",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected chdb unavailable error")
	}
	for _, want := range []string{"chdb storage adapter is not linked", "-tags chdb", "libchdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
