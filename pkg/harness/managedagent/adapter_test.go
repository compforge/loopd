package managedagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
)

const inputEvent = `{"id":"input-1","type":"user.message","content":[{"type":"text","text":"hello"}],"processed_at":null}`
const answerEvent = `{"id":"answer-1","type":"agent.message","content":[{"type":"text","text":"hello world"}],"processed_at":"2026-09-06T00:00:00Z"}`
const endEvent = `{"id":"idle-1","type":"session.status_idle","stop_reason":{"type":"end_turn"}}`

func testConfig(url string) Config {
	return Config{APIKey: "test-key", BaseURL: url, AgentID: "agent-1", EnvironmentID: "env-1",
		HTTPTimeout: time.Second, StreamTimeout: 3 * time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func testRequest() harness.Request {
	return harness.Request{CallID: "call-1", IdempotencyKey: "business-step-1", Prompt: "hello"}
}

func writeEvents(w http.ResponseWriter, events ...string) {
	for _, event := range events {
		var header struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(event), &header)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", header.Type, event)
	}
	w.(http.Flusher).Flush()
}

// The real SDK talks to a protocol fixture, not an agentd-specific client or mock.
func TestPromptStreamsThroughOfficialSDK(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" || !strings.Contains(r.Header.Get("anthropic-beta"), "managed-agents") {
			t.Error("missing SDK authentication or Managed Agents beta header")
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("adapter must not assume a backend-specific idempotency extension")
		}
		switch r.URL.Path {
		case "/v1/sessions":
			if r.Method != http.MethodPost {
				t.Errorf("session method = %s", r.Method)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["agent"]) != `"agent-1"` || string(body["environment_id"]) != `"env-1"` {
				t.Errorf("wrong Session resource binding: %s / %s", body["agent"], body["environment_id"])
			}
			var initial []struct {
				Type    string `json:"type"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(body["initial_events"], &initial); err != nil {
				t.Error(err)
			}
			if len(initial) != 1 || initial[0].Type != "user.message" || len(initial[0].Content) != 1 || initial[0].Content[0].Text != "hello" {
				t.Errorf("wrong initial input: %s", body["initial_events"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"session-1","type":"session"}`)
		case "/v1/sessions/session-1/events":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, inputEvent)
		case "/v1/sessions/session-1/events/stream":
			if r.URL.Query().Get("event_deltas[]") != "agent.message" {
				t.Error("missing preview request")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeEvents(w, inputEvent,
				`{"type":"event_start","event":{"type":"agent.message","id":"answer-1"}}`,
				`{"type":"event_delta","event_id":"answer-1","delta":{"type":"content_delta","index":0,"content":{"type":"text","text":"hello"}}}`)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			writeEvents(w, answerEvent, endEvent)
		default:
			t.Errorf("unexpected SDK path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config := testConfig(server.URL)
	config.PreviewDeltas = true
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prompt(ctx, testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if call.ID() != "call-1" {
		t.Fatalf("call ID = %s", call.ID())
	}
	select {
	case event := <-call.Events():
		if !strings.Contains(string(event.Data), `"content":"hello"`) {
			t.Fatalf("preview = %s", event.Data)
		}
	case <-ctx.Done():
		t.Fatal("no preview before model completion")
	}
	waitCtx, stopWait := context.WithCancel(ctx)
	stopWait()
	if _, err := call.Wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(release)
	var final string
	for event := range call.Events() {
		final = string(event.Data)
	}
	if !strings.Contains(final, `"content":"hello world"`) {
		t.Fatalf("final update = %s", final)
	}
	for range 2 {
		result, err := call.Wait(ctx)
		if err != nil || result.Text != "hello world" {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
	}
}

func TestPromptInitialHistoryAndStops(t *testing.T) {
	tests := []struct {
		name, history, live string
		wantError           bool
	}{
		{"already completed", inputEvent + "," + answerEvent + "," + endEvent, "", false},
		{"live completed", inputEvent, answerEvent + "\n" + endEvent, false},
		{"needs external action", inputEvent, `{"id":"idle-1","type":"session.status_idle","stop_reason":{"type":"requires_action"}}`, true},
		{"remote error", inputEvent, `{"id":"err-1","type":"session.error","error":{"type":"execution_error","retry_status":{"type":"not_retryable"}}}`, true},
		{"truncated stream", inputEvent, answerEvent, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/sessions":
					_, _ = io.WriteString(w, `{"id":"session-1"}`)
				case "/v1/sessions/session-1/events":
					_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, test.history)
				case "/v1/sessions/session-1/events/stream":
					if r.URL.Query().Has("event_deltas[]") {
						t.Error("unexpected previews for durable-only backend")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if test.live != "" {
						writeEvents(w, strings.Split(test.live, "\n")...)
					} else {
						w.(http.Flusher).Flush()
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			adapter, err := New(testConfig(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			call, err := adapter.Prompt(context.Background(), testRequest())
			if err != nil {
				t.Fatal(err)
			}
			for range call.Events() {
			}
			result, err := call.Wait(context.Background())
			if (err != nil) != test.wantError {
				t.Fatalf("result = %#v, err = %v", result, err)
			}
			if !test.wantError && result.Text != "hello world" {
				t.Fatalf("text = %q", result.Text)
			}
		})
	}
}

func TestPromptDoesNotRetrySessionCreation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"failed"}}`)
	}))
	defer server.Close()
	adapter, err := New(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prompt(context.Background(), testRequest()); err == nil {
		t.Fatal("expected submission error")
	}
	if requests.Load() != 1 {
		t.Fatalf("Session submitted %d times", requests.Load())
	}
}

func TestPromptTimeoutStopsOnlyObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions":
			_, _ = io.WriteString(w, `{"id":"session-1"}`)
		case "/v1/sessions/session-1/events":
			if r.Method != http.MethodGet {
				t.Error("observation cancellation must not send remote input")
			}
			_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false}`, inputEvent)
		case "/v1/sessions/session-1/events/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			t.Errorf("unexpected remote mutation: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	adapter, err := New(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest()
	request.Timeout = 200 * time.Millisecond
	call, err := adapter.Prompt(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for range call.Events() {
	}
	if _, err := call.Wait(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call timeout = %v", err)
	}
}

func TestPromptConfiguresRemoteTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		agent, ok := body["agent"].(map[string]any)
		if !ok || agent["type"] != "agent_with_overrides" || len(agent["tools"].([]any)) != 1 {
			t.Errorf("agent override = %#v", body["agent"])
		}
		// Stop after verifying the SDK request; no remote execution is needed.
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	config := testConfig(server.URL)
	request := testRequest()
	request.Tools = []contract.Tool{{Name: "remote-tools"}}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prompt(context.Background(), request); err == nil {
		t.Fatal("must not silently ignore requested tools")
	}
	config.ConfigureSession = func(_ context.Context, request harness.Request, params *anthropic.BetaSessionNewParams) error {
		if request.Tools[0].Name != "remote-tools" {
			t.Fatal("missing tool descriptor")
		}
		params.Agent = anthropic.BetaSessionNewParamsAgentUnion{
			OfBetaManagedAgentsAgentWithOverridess: &anthropic.BetaManagedAgentsAgentWithOverridesParams{
				ID: "agent-1", Type: anthropic.BetaManagedAgentsAgentWithOverridesParamsTypeAgentWithOverrides,
				Tools: []anthropic.BetaManagedAgentsAgentWithOverridesParamsToolUnion{{
					OfAgentToolset20260401: &anthropic.BetaManagedAgentsAgentToolset20260401Params{Type: anthropic.BetaManagedAgentsAgentToolset20260401ParamsTypeAgentToolset20260401},
				}},
			},
		}
		return nil
	}
	adapter, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prompt(context.Background(), request); err == nil {
		t.Fatal("expected fixture rejection after verifying override")
	}
}

func TestResumeReattachesAndAmbiguousCreateRequiresIdempotency(t *testing.T) {
	var creates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sessions":
			creates.Add(1)
			if r.Header.Get("Idempotency-Key") != "business-step-1" {
				t.Error("missing stable idempotency key")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"session-1","type":"session"}`)
		case "/v1/sessions/session-1/events":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[%s,%s,%s],"has_more":false}`, inputEvent, answerEvent, endEvent)
		case "/v1/sessions/session-1/events/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			writeEvents(w, inputEvent, answerEvent, endEvent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter, err := New(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Resume(context.Background(), testRequest()); !errors.Is(err, harness.ErrRecoveryUnsupported) {
		t.Fatalf("unsafe recovery=%v", err)
	}
	request := testRequest()
	request.ExecutionRef = "session-1"
	call, err := adapter.Resume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for range call.Events() {
	}
	result, err := call.Wait(context.Background())
	if err != nil || result.Text != "hello world" || creates.Load() != 0 {
		t.Fatalf("reattach=%+v creates=%d err=%v", result, creates.Load(), err)
	}
	config := testConfig(server.URL)
	config.IdempotentSubmission = true
	adapter, _ = New(config)
	call, err = adapter.Resume(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	for range call.Events() {
	}
	if _, err := call.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatalf("creates=%d", creates.Load())
	}
}
