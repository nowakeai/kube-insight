package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestNewEvidenceCondenserTool(t *testing.T) {
	ctx := context.Background()
	condenser, err := NewEvidenceCondenserTool(ctx, &capabilityStaticModel{answer: "- artifact_sql: vmagent has OOMKilled facts"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := condenser.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != evidenceCondenserToolName || !strings.Contains(strings.ToLower(info.Desc), "condense") {
		t.Fatalf("tool info = %#v", info)
	}
	invokable, ok := condenser.(tool.InvokableTool)
	if !ok {
		t.Fatalf("condenser is not invokable: %T", condenser)
	}
	out, err := invokable.InvokableRun(ctx, `{"request":"summarize artifact_sql"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "artifact_sql") || !strings.Contains(out, "OOMKilled") {
		t.Fatalf("condenser output = %q", out)
	}
}

func TestEvidenceCondenserInstructionIsEvidenceBounded(t *testing.T) {
	instruction := EvidenceCondenserInstruction()
	for _, want := range []string{"Do not fetch new Kubernetes data", "artifact IDs", "do not invent facts", "Sources line", "source artifact"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q: %s", want, instruction)
		}
	}
}
