package managedagent

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	ui "github.com/compforge/agentue/sdks/go/ui"
)

func projectJSON(t *testing.T, state *projection, raw string) ([]ui.Event, bool, error) {
	t.Helper()
	var event anthropic.BetaManagedAgentsStreamSessionEventsUnion
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatal(err)
	}
	return state.consume(event)
}

func TestProjectionKeepsCanonicalOutput(t *testing.T) {
	state := &projection{sessionID: "session-1", seen: map[string]bool{}}
	// A newly created Session can be idle before the initial input is accepted.
	if _, done, err := projectJSON(t, state, endEvent); done || err != nil {
		t.Fatal("initial idle finished the call")
	}
	projectJSON(t, state, inputEvent)
	updates, _, err := projectJSON(t, state, answerEvent)
	if err != nil || len(updates) != 1 {
		t.Fatalf("answer updates: %v, %v", updates, err)
	}
	for _, raw := range []string{
		inputEvent, answerEvent,
		`{"type":"event_start","event":{"type":"agent.message","id":"answer-1"}}`,
		`{"type":"event_delta","event_id":"answer-1","delta":{"type":"content_delta","index":0,"content":{"type":"text","text":"old preview"}}}`,
	} {
		updates, done, err := projectJSON(t, state, raw)
		if err != nil || done || len(updates) != 0 {
			t.Fatalf("duplicate or stale preview changed output: %v, %v, %v", updates, done, err)
		}
	}
	if state.text != "hello world" {
		t.Fatalf("result = %q", state.text)
	}
	if _, done, err := projectJSON(t, state, endEvent); !done || err != nil {
		t.Fatalf("completion: %v, %v", done, err)
	}
}

func TestProjectionToolLifecycle(t *testing.T) {
	state := &projection{sessionID: "session-1", seen: map[string]bool{}}
	projectJSON(t, state, inputEvent)
	updates, _, err := projectJSON(t, state, `{"id":"tool-1","type":"agent.tool_use","name":"bash","input":{"command":"pwd"}}`)
	if err != nil || len(updates) != 1 {
		t.Fatalf("tool use: %v, %v", updates, err)
	}
	id := updates[0].Block["id"]
	if updates[0].Block["status"] != "running" || updates[0].Block["name"] != "bash" {
		t.Fatalf("tool block: %v", updates[0].Block)
	}
	updates, _, err = projectJSON(t, state, `{"id":"result-1","type":"agent.tool_result","tool_use_id":"tool-1","is_error":false,"content":[]}`)
	if err != nil || len(updates) != 1 {
		t.Fatalf("tool result: %v, %v", updates, err)
	}
	if updates[0].Block["id"] != id || updates[0].Block["status"] != "completed" {
		t.Fatalf("tool result targeted wrong block: %v", updates[0].Block)
	}
}
