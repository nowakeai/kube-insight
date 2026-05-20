package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestArtifactAndCitationContractNames(t *testing.T) {
	artifactKinds := []string{
		ArtifactKindMarkdown,
		ArtifactKindK8sResource,
		ArtifactKindK8sResourceList,
		ArtifactKindK8sTopology,
		ArtifactKindK8sHistory,
		ArtifactKindK8sDiff,
		ArtifactKindToolCall,
		ArtifactKindCitation,
	}
	assertUniqueNonEmpty(t, "artifact kind", artifactKinds)
	if ArtifactKindK8sResource != "k8s.resource" || ArtifactKindK8sTopology != "k8s.topology" || ArtifactKindCitation != "citation" {
		t.Fatalf("unexpected artifact contract names: %#v", artifactKinds)
	}

	citationTargets := []string{
		CitationTargetObject,
		CitationTargetVersion,
		CitationTargetFact,
		CitationTargetChange,
		CitationTargetEdge,
		CitationTargetSQLRow,
		CitationTargetArtifact,
	}
	assertUniqueNonEmpty(t, "citation target", citationTargets)
	if CitationTargetObject != "object" || CitationTargetSQLRow != "sql_row" || CitationTargetArtifact != "artifact" {
		t.Fatalf("unexpected citation target names: %#v", citationTargets)
	}
}

func TestDefaultAgentInstructionRequiresEvidenceCitations(t *testing.T) {
	instruction := DefaultAgentInstruction()
	for _, want := range []string{"kube-insight Kubernetes investigation agent", "Check collector health", "cite the exact proof", "version IDs", "Evidence section"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("default instruction missing %q: %s", want, instruction)
		}
	}
}

func TestToolDescriptionsIncludeCitationGuidance(t *testing.T) {
	tools := []struct {
		name string
		tool interface {
			Info(context.Context) (*schema.ToolInfo, error)
		}
	}{
		{name: HealthToolName, tool: NewHealthTool(nil, HealthToolOptions{})},
		{name: SearchToolName, tool: NewSearchTool(nil, SearchToolOptions{})},
		{name: HistoryToolName, tool: NewHistoryTool(nil)},
		{name: TopologyToolName, tool: NewTopologyTool(nil)},
		{name: ServiceInvestigationToolName, tool: NewServiceInvestigationTool(nil)},
		{name: SQLToolName, tool: NewSQLTool(nil)},
	}
	for _, item := range tools {
		info, err := item.tool.Info(context.Background())
		if err != nil {
			t.Fatalf("%s Info: %v", item.name, err)
		}
		if !strings.Contains(info.Desc, "Cite concrete proof") {
			t.Fatalf("%s description missing citation guidance: %s", item.name, info.Desc)
		}
	}
}

func assertUniqueNonEmpty(t *testing.T, label string, values []string) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("empty %s in %#v", label, values)
		}
		if _, ok := seen[value]; ok {
			t.Fatalf("duplicate %s %q in %#v", label, value, values)
		}
		seen[value] = struct{}{}
	}
}
