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
		"CODE_OF_CONDUCT.md",
		"CONTRIBUTING.md",
		"MAINTAINERS.md",
		"SUPPORT.md",
		"SECURITY.md",
		"RELEASE.md",
		"Dockerfile",
		".github/dependabot.yml",
		".github/pull_request_template.md",
		".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/config.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml",
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
