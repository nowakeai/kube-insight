package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestEinoRunnerRunsChatModelAgent(t *testing.T) {
	ctx := context.Background()
	fake := &fakeEinoModel{answer: "checked cluster health"}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Description: "Kubernetes investigation assistant",
		Instruction: "Answer with concise evidence.",
		Model:       fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: "is the api healthy?"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "checked cluster health" || result.Events != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(fake.inputs) != 2 || fake.inputs[0].Role != schema.System || fake.inputs[1].Role != schema.User || fake.inputs[1].Content != "is the api healthy?" {
		t.Fatalf("model inputs = %#v", fake.inputs)
	}
}

func TestNewEinoRunnerRequiresModel(t *testing.T) {
	_, err := NewEinoRunner(context.Background(), EinoRunnerConfig{})
	if !errors.Is(err, ErrEinoModelRequired) {
		t.Fatalf("err = %v", err)
	}
}

type fakeEinoModel struct {
	answer string
	inputs []*schema.Message
}

func (m *fakeEinoModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append([]*schema.Message(nil), input...)
	return schema.AssistantMessage(m.answer, nil), nil
}

func (m *fakeEinoModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream is not implemented in fakeEinoModel")
}
