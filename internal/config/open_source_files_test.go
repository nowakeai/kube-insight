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
		"LICENSE",
		"CODE_OF_CONDUCT.md",
		"CONTRIBUTING.md",
		"MAINTAINERS.md",
		"SUPPORT.md",
		"SECURITY.md",
		"RELEASE.md",
		"Dockerfile",
		"docker/chdb.Dockerfile",
		".dockerignore",
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

func TestGoReleaserPublishesExpectedArtifactsAndImages(t *testing.T) {
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
		"id: kube-insight",
		"CGO_ENABLED=0",
		"ignore:",
		"goos: windows",
		"goarch: arm64",
		"id: kube-insight-chdb-linux-amd64",
		"id: kube-insight-chdb-linux-arm64",
		"id: kube-insight-chdb-darwin-amd64",
		"id: kube-insight-chdb-darwin-arm64",
		"CGO_ENABLED=0",
		"- chdb",
		"build/chdb-runtime/linux-amd64/libchdb.so",
		"build/chdb-runtime/linux-arm64/libchdb.so",
		"build/chdb-runtime/darwin-amd64/libchdb.so",
		"build/chdb-runtime/darwin-arm64/libchdb.so",
		"config/kube-insight.chdb.example.yaml",
		"dockers_v2:",
		"ghcr.io/nowakeai/kube-insight",
		"{{ .Tag }}-chdb",
		"latest-chdb",
		"linux/amd64",
		"linux/arm64",
		"build/chdb-runtime/libchdb-linux-amd64.so",
		"build/chdb-runtime/libchdb-linux-arm64.so",
		"docker/chdb.Dockerfile",
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
		"CHDB_VERSION",
		"linux-x86_64-libchdb",
		"linux-aarch64-libchdb",
		"macos-x86_64-libchdb",
		"macos-arm64-libchdb",
		"build/chdb-runtime/libchdb-linux-amd64.so",
		"build/chdb-runtime/libchdb-linux-arm64.so",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release workflow missing %q", want)
		}
	}
}
