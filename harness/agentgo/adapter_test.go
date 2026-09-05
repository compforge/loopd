package agentgo

import (
	"context"
	"errors"
	"testing"
	"time"

	agent "github.com/compforge/agentgo"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/harness"
)

func TestAdapterPromptStreamsTextAndReturnsResult(t *testing.T) {
	adapter, err := New(func(context.Context, harness.Request) (*agent.Agent, error) {
		return agent.NewAgent(agent.WithModel(stubModel{reply: "hello"})), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prompt(context.Background(), harness.Request{
		CallID: "call-1", IdempotencyKey: "task-1/answer", Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	var streamed, final string
	for value := range call.Events() {
		event, err := agentueui.Parse(value.Data)
		if err != nil {
			t.Fatal(err)
		}
		if event.Op == agentueui.OpAppend {
			streamed += event.Block["content"].(string)
		}
		if event.Op == agentueui.OpSet && event.Block["id"] == "harness/call-1/answer" {
			final = event.Block["content"].(string)
		}
	}
	result, err := call.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if streamed != "hello" || final != "hello" || result.Text != "hello" {
		t.Fatalf("streamed text = %q, final text = %q, result = %#v", streamed, final, result)
	}
}

type stubModel struct {
	reply string
}

func (model stubModel) Generate(context.Context, []agent.Message, []agent.ToolSpec, ...agent.CallOption) (*agent.LLMResponse, error) {
	return &agent.LLMResponse{Message: model.message()}, nil
}

func (model stubModel) GenerateStream(context.Context, []agent.Message, []agent.ToolSpec, ...agent.CallOption) (<-chan agent.StreamEvent, error) {
	events := make(chan agent.StreamEvent, 4)
	partial := agent.Message{Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock("")}}
	events <- agent.StreamEvent{Type: agent.StreamEventTextStart, Message: partial}
	events <- agent.StreamEvent{Type: agent.StreamEventTextDelta, Delta: model.reply, Message: partial}
	events <- agent.StreamEvent{Type: agent.StreamEventTextEnd, Message: partial}
	events <- agent.StreamEvent{Type: agent.StreamEventDone, Message: model.message(), StopReason: agent.StopReasonStop}
	close(events)
	return events, nil
}

func (stubModel) SupportsTools() bool { return true }

func (model stubModel) message() agent.Message {
	return agent.Message{
		Role: agent.RoleAssistant, Content: []agent.ContentBlock{agent.TextBlock(model.reply)},
		StopReason: agent.StopReasonStop,
	}
}

type blockedModel struct{ stubModel }

func (blockedModel) GenerateStream(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec, _ ...agent.CallOption) (<-chan agent.StreamEvent, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestAdapterEnforcesExecutionTimeout(t *testing.T) {
	adapter, err := New(func(ctx context.Context, _ harness.Request) (*agent.Agent, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("execution has no deadline")
		}
		return agent.NewAgent(agent.WithModel(blockedModel{})), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prompt(context.Background(), harness.Request{CallID: "timeout", IdempotencyKey: "task/timeout", Prompt: "wait", Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = call.Wait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("Adapter did not enforce its own timeout: %v, wait=%v", err, ctx.Err())
	}
}
