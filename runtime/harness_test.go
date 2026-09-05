package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/harness"
)

func TestHarnessPromptPublishesEventsAndReusesEffect(t *testing.T) {
	var (
		publishedMu sync.Mutex
		published   []agentueui.Event
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/tasks/task-1/events" {
			http.NotFound(response, request)
			return
		}
		var input struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		event, err := agentueui.Parse(input.Event)
		if err != nil {
			t.Error(err)
			return
		}
		publishedMu.Lock()
		published = append(published, event)
		publishedMu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"id":%q}`, fmt.Sprintf("%d-0", event.Seq))
	}))
	t.Cleanup(server.Close)

	adapter := &fakeHarnessAdapter{}
	runtime, err := New(server.URL, Options{
		HTTPClient: server.Client(),
		Harnesses:  map[string]harness.Adapter{"demo": adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	operatorEvent, err := runtime.Loop.Chat.Emit(context.Background(), "task-1", agentueui.Event{
		Op: agentueui.OpSet,
		Block: map[string]any{
			"id": "operator/status", "type": "text", "content": "routing",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	parsedOperatorOutput, err := agentueui.Parse(operatorEvent.Data)
	if err != nil || parsedOperatorOutput.Seq != 2 {
		t.Fatalf("Operator event = %#v, error=%v", parsedOperatorOutput, err)
	}

	prompt := Prompt{TaskID: "task-1", EffectKey: "route", Target: "demo", Text: "hello"}
	call, err := runtime.Loop.Harness.Prompt(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := call.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != "done" || result.Phase != "succeeded" {
		t.Fatalf("Call = %#v", result)
	}

	stream, streamErrors := call.Stream(context.Background(), "")
	var observed []string
	for event := range stream {
		observed = append(observed, event.ID)
	}
	if err := <-streamErrors; err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(observed), "[3-0 4-0 5-0]"; got != want {
		t.Fatalf("event IDs = %s, want %s", got, want)
	}
	publishedMu.Lock()
	if len(published) != 4 || published[0].Seq != 2 || published[1].Seq != 3 || published[2].Seq != 4 || published[3].Seq != 5 {
		t.Fatalf("published events = %#v", published)
	}
	publishedMu.Unlock()

	again, err := runtime.Loop.Harness.Prompt(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	if again != call || adapter.starts != 1 {
		t.Fatalf("reused Call = %t, Adapter starts = %d", again == call, adapter.starts)
	}
	for _, end := range []int{3, 4} {
		snapshot, err := agentueui.ApplyAll(map[string]any{"blocks": []any{}}, published[1:end])
		if err != nil {
			t.Fatal(err)
		}
		block := snapshot["blocks"].([]any)[0].(map[string]any)
		if block["call_id"] != result.ID || block["effect_key"] != "route" || block["content"] != "hello" {
			t.Fatalf("streaming/final Harness block = %#v", block)
		}
	}
	changed := prompt
	changed.Text = "different"
	if _, err := runtime.Loop.Harness.Prompt(context.Background(), changed); !errors.Is(err, ErrCallConflict) {
		t.Fatalf("changed prompt error = %v, want ErrCallConflict", err)
	}
}

type fakeHarnessAdapter struct {
	starts int
}

func (adapter *fakeHarnessAdapter) Prompt(_ context.Context, request harness.Request) (harness.Call, error) {
	adapter.starts++
	events := make(chan harness.Event, 3)
	for _, event := range []agentueui.Event{
		{Op: agentueui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "answer", "type": "text", "content": "hel"}},
		{Op: agentueui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "answer", "content": "lo"}},
		{Op: agentueui.OpSet, Block: map[string]any{"id": "answer", "type": "text", "content": "hello"}},
	} {
		data, err := event.Marshal()
		if err != nil {
			return nil, err
		}
		events <- harness.Event{Data: data}
	}
	close(events)
	return fakeHarnessCall{id: request.CallID, events: events}, nil
}

type fakeHarnessCall struct {
	id     string
	events <-chan harness.Event
}

func (call fakeHarnessCall) ID() string                   { return call.id }
func (call fakeHarnessCall) Events() <-chan harness.Event { return call.events }
func (call fakeHarnessCall) Wait(context.Context) (harness.Result, error) {
	return harness.Result{Text: "done"}, nil
}
