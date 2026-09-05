// Package agentgo provides the in-process AgentGo Harness demo Adapter.
// Production deployments should use agentd for durable Sessions and recovery.
package agentgo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	agent "github.com/compforge/agentgo"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/harness"
)

// Factory builds one process-local Agent for a Call. It resolves the requested
// tool descriptors to concrete AgentGo tools and owns model configuration.
type Factory func(context.Context, harness.Request) (*agent.Agent, error)

type Adapter struct {
	factory Factory
}

func New(factory Factory) (*Adapter, error) {
	if factory == nil {
		return nil, errors.New("agentgo harness factory is required")
	}
	return &Adapter{factory: factory}, nil
}

func (adapter *Adapter) Prompt(ctx context.Context, request harness.Request) (harness.Call, error) {
	parent := ctx
	ctx, cancel := context.WithCancel(parent)
	if request.Timeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(parent, request.Timeout)
	}
	started := false
	defer func() {
		if !started {
			cancel()
		}
	}()
	if request.CallID == "" || request.IdempotencyKey == "" || request.Prompt == "" {
		return nil, errors.New("call ID, idempotency key, and prompt are required")
	}
	instance, err := adapter.factory(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("build agentgo harness: %w", err)
	}
	if instance == nil {
		return nil, errors.New("agentgo harness factory returned nil")
	}
	call := &call{
		id: request.CallID, agent: instance, cancel: cancel,
		events: make(chan harness.Event, 128), done: make(chan struct{}),
	}
	instance.Subscribe(call.consume)
	if err := instance.Prompt(ctx, request.Prompt); err != nil {
		call.finish(err)
		return nil, err
	}
	started = true
	return call, nil
}

type call struct {
	cancel        context.CancelFunc
	id            string
	agent         *agent.Agent
	events        chan harness.Event
	done          chan struct{}
	once          sync.Once
	mu            sync.Mutex
	result        harness.Result
	err           error
	projectionErr error
}

func (call *call) ID() string                   { return call.id }
func (call *call) Events() <-chan harness.Event { return call.events }

func (call *call) Wait(ctx context.Context) (harness.Result, error) {
	select {
	case <-ctx.Done():
		return harness.Result{}, ctx.Err()
	case <-call.done:
		call.mu.Lock()
		defer call.mu.Unlock()
		return call.result, call.err
	}
}

func (call *call) consume(event agent.Event) {
	if event.Type == agent.EventMessageEnd && event.Message != nil && event.Message.GetRole() == agent.RoleAssistant {
		call.mu.Lock()
		call.result.Text = event.Message.TextContent()
		call.mu.Unlock()
	}
	if projection, ok := project(call.id, event); ok {
		data, err := projection.Marshal()
		if err != nil {
			call.mu.Lock()
			call.projectionErr = fmt.Errorf("encode AgentGo projection: %w", err)
			call.mu.Unlock()
			call.agent.AbortSilent()
			return
		}
		call.events <- harness.Event{Data: data}
	}
	if event.Type == agent.EventAgentEnd {
		call.mu.Lock()
		projectionErr := call.projectionErr
		call.mu.Unlock()
		call.finish(errors.Join(projectionErr, event.Err))
	}
}

func (call *call) finish(err error) {
	call.once.Do(func() {
		if call.cancel != nil {
			call.cancel()
		}
		call.mu.Lock()
		call.err = err
		call.mu.Unlock()
		close(call.events)
		close(call.done)
	})
}

func project(callID string, event agent.Event) (agentueui.Event, bool) {
	answerID := "harness/" + callID + "/answer"
	switch event.Type {
	case agent.EventMessageUpdate:
		if event.Delta == "" || event.DeltaKind != agent.DeltaText {
			return agentueui.Event{}, false
		}
		return agentueui.Event{
			Op: agentueui.OpAppend, Mask: "block.content",
			Block: map[string]any{"id": answerID, "type": "text", "content": event.Delta},
		}, true
	case agent.EventMessageEnd:
		if event.Message == nil || event.Message.GetRole() != agent.RoleAssistant {
			return agentueui.Event{}, false
		}
		return agentueui.Event{
			Op: agentueui.OpSet,
			Block: map[string]any{
				"id": answerID, "type": "text", "content": event.Message.TextContent(),
			},
		}, true
	case agent.EventToolExecStart:
		return agentueui.Event{
			Op: agentueui.OpSet,
			Block: map[string]any{
				"id": toolBlockID(callID, event.ToolID), "type": "tool",
				"name": event.Tool, "status": "running",
			},
		}, true
	case agent.EventToolExecEnd:
		status := "completed"
		if event.IsError {
			status = "failed"
		}
		return agentueui.Event{
			Op: agentueui.OpSet, Mask: "block.status",
			Block: map[string]any{"id": toolBlockID(callID, event.ToolID), "status": status},
		}, true
	default:
		return agentueui.Event{}, false
	}
}

func toolBlockID(callID, toolID string) string {
	if toolID == "" {
		toolID = "unknown"
	}
	return "harness/" + callID + "/tool/" + toolID
}

var _ harness.Adapter = (*Adapter)(nil)
