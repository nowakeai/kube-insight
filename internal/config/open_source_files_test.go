package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenSourceReleaseFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, path := range []string{
		"README.md",
		"CONTRIBUTING.md",
		"SECURITY.md",
		"RELEASE.md",
		"Dockerfile",
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
		".goreleaser.yaml",
		"assets/brand/kube-insight-logo.svg",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestGoReleaserPublishesExpectedGHCRImages(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["project_name"] != "kube-insight" {
		t.Fatalf("project_name = %#v", cfg["project_name"])
	}
	text := string(data)
	for _, want := range []string{
		"ghcr.io/nowakeai/kube-insight:{{ .Tag }}",
		"ghcr.io/nowakeai/kube-insight:latest",
		"--platform=linux/amd64",
		"--platform=linux/arm64",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf(".goreleaser.yaml missing %q", want)
		}
	}
}

func TestReleaseWorkflowUsesGoReleaserAndGHCR(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"goreleaser/goreleaser-action",
		"docker/login-action",
		"registry: ghcr.io",
		"packages: write",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release workflow missing %q", want)
		}
	}
}
