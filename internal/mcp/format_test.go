package mcp

import (
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestFormatResourceHealthDSLCompactsProblemRowsAndErrors(t *testing.T) {
	longError := strings.Repeat("watch stream internal error ", 20)
	report := storage.ResourceHealthReport{
		Summary:  storage.ResourceHealthSummary{Resources: 3, Healthy: 1, Unstable: 2},
		ByStatus: map[string]int{"watching": 1, "retrying": 2},
		Resources: []storage.ResourceHealthRecord{
			{ClusterID: "k8s-abc", ClusterUID: "abc", ClusterSource: "gke_project_region_cluster https://10.0.0.1", Resource: "pods", Version: "v1", Kind: "Pod", Status: "retrying", Error: longError},
			{ClusterID: "k8s-abc", ClusterUID: "abc", ClusterSource: "gke_project_region_cluster https://10.0.0.1", Resource: "jobs", Group: "batch", Version: "v1", Kind: "Job", Status: "retrying", Error: longError},
		},
	}

	text := formatResourceHealthDSL(report, 1)
	if !strings.Contains(text, "clusters:") || !strings.Contains(text, "display=gke_project_region_cluster") || !strings.Contains(text, "id=k8s-abc") {
		t.Fatalf("missing cluster display map:\n%s", text)
	}
	if strings.Count(text, "resource=") != 1 {
		t.Fatalf("expected one problem row, got:\n%s", text)
	}
	if !strings.Contains(text, "problem_resources_omitted: 1") {
		t.Fatalf("missing omitted count:\n%s", text)
	}
	if !strings.Contains(text, "error=") || !strings.Contains(text, "...") {
		t.Fatalf("expected truncated error:\n%s", text)
	}
	if strings.Contains(text, strings.Repeat("watch stream internal error ", 8)) {
		t.Fatalf("error was not compacted enough:\n%s", text)
	}
}
