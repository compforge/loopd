package managedagent

import (
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	ui "github.com/compforge/agentue/sdks/go/ui"
)

// StopError reports a remote stop which is not successful completion. In
// particular requires_action must never be turned into approval by the Adapter.
type StopError struct {
	SessionID string
	Reason    string
}

func (err *StopError) Error() string {
	return fmt.Sprintf("managed Session %s stopped: %s", err.SessionID, err.Reason)
}

type projection struct {
	sessionID string
	started   bool
	seen      map[string]bool
	previews  anthropic.BetaManagedAgentsEventAccumulator
	text      string
}

func (state *projection) consume(event anthropic.BetaManagedAgentsStreamSessionEventsUnion) ([]ui.Event, bool, error) {
	if !state.started {
		if event.Type == "user.message" {
			state.started = true
			state.seen[event.ID] = true
		}
		return nil, false, nil
	}
	if event.ID != "" {
		if state.seen[event.ID] {
			return nil, false, nil
		}
		state.seen[event.ID] = true
	}
	blockID := func(id string) string { return "managed/" + state.sessionID + "/" + id }
	textEvent := func(id, text string) []ui.Event {
		return []ui.Event{{Op: ui.OpSet, Block: map[string]any{"id": blockID(id), "type": "text", "content": text}}}
	}
	switch event.Type {
	case "user.message":
		return nil, true, &StopError{state.sessionID, "another input arrived in an exclusively bound Session"}
	case "event_start":
		// Initial catch-up can carry history before buffered live previews. Never let
		// an old preview overwrite an already accepted canonical message.
		if !state.seen[event.Event.ID] {
			state.previews.Accumulate(event)
		}
	case "event_delta":
		if state.seen[event.EventID] {
			return nil, false, nil
		}
		state.previews.Accumulate(event)
		if _, exists := state.previews.AgentMessages[event.EventID]; exists {
			return textEvent(event.EventID, state.previews.AgentMessageText(event.EventID)), false, nil
		}
	case "agent.message":
		state.previews.Accumulate(event)
		state.text = state.previews.AgentMessageText(event.ID)
		delete(state.previews.AgentMessages, event.ID)
		return textEvent(event.ID, state.text), false, nil
	case "span.model_request_end":
		state.previews.Accumulate(event)
	case "agent.tool_use", "agent.custom_tool_use", "agent.mcp_tool_use":
		var name string
		var input map[string]any
		switch event.Type {
		case "agent.tool_use":
			value := event.AsAgentToolUse()
			name, input = value.Name, value.Input
		case "agent.custom_tool_use":
			value := event.AsAgentCustomToolUse()
			name, input = value.Name, value.Input
		case "agent.mcp_tool_use":
			value := event.AsAgentMCPToolUse()
			name, input = value.Name, value.Input
		}
		return []ui.Event{{Op: ui.OpSet, Block: map[string]any{
			"id": blockID(event.ID), "type": "tool", "name": name, "input": input, "status": "running",
		}}}, false, nil
	case "agent.tool_result", "agent.mcp_tool_result":
		var useID string
		var failed bool
		if event.Type == "agent.tool_result" {
			value := event.AsAgentToolResult()
			useID, failed = value.ToolUseID, value.IsError
		} else {
			value := event.AsAgentMCPToolResult()
			useID, failed = value.MCPToolUseID, value.IsError
		}
		status := "completed"
		if failed {
			status = "failed"
		}
		return []ui.Event{{Op: ui.OpSet, Mask: "block.status", Block: map[string]any{"id": blockID(useID), "status": status}}}, false, nil
	case "session.error":
		value := event.AsSessionError()
		if value.Error.RetryStatus.Type != "retrying" {
			return nil, true, &StopError{state.sessionID, "execution error: " + value.Error.Type}
		}
	case "session.status_idle":
		reason := event.AsSessionStatusIdle().StopReason.Type
		if reason == "end_turn" {
			return nil, true, nil
		}
		return nil, true, &StopError{state.sessionID, reason}
	case "session.status_terminated", "session.deleted":
		return nil, true, &StopError{state.sessionID, event.Type}
	}
	return nil, false, nil
}
