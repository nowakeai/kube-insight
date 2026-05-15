package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunConfigValidateTable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "validate"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"setting",
		"value",
		"ok",
		"version",
		"v1alpha1",
		"processing",
		"filters",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunConfigValidateJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	path := filepath.Join("..", "..", "config", "kube-insight.example.yaml")
	err := Run(context.Background(), []string{"config", "validate", "--file", path, "--output", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"ok": true`,
		`"file": "../../config/kube-insight.example.yaml"`,
		`"version": "v1alpha1"`,
		`"filterChains": 6`,
		`"filters": 12`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunConfigShowEffectiveYAML(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--log-level", "debug", "config", "show"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"version: v1alpha1",
		"logging:",
		"level: debug",
		"resourceProfiles:",
		"replaceDefaults: true",
		"name: pod_fast_path",
		"name: lease_skip_or_downsample",
		"processing:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunConfigShowEffectiveJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--collection-enabled", "config", "show", "--output", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"version": "v1alpha1"`,
		`"collection": {`,
		`"enabled": true`,
		`"replaceDefaults": true`,
		`"name": "pod_fast_path"`,
		`"processing": {`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunConfigCatalogTable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "catalog"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type",
		"required action",
		"filter",
		"managed_fields",
		"keep_modified",
		"extractor",
		"endpointslice",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunConfigCatalogJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"config", "catalog", "--output", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"filters": [`,
		`"name": "managed_fields"`,
		`"action": "keep_modified"`,
		`"extractors": [`,
		`"name": "endpointslice"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}
