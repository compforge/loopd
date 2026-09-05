package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/harness"
	loopruntime "github.com/compforge/loopd/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestReconcileRoutesSimpleAndComplexTasks(t *testing.T) {
	tests := []struct {
		name        string
		plan        string
		results     map[string]string
		wantEffects []string
		wantAnswer  string
	}{
		{
			name: "simple",
			plan: `{"kind":"simple","tasks":["explain UUIDv7 ordering"]}`,
			results: map[string]string{
				"work/0":    "UUIDv7 starts with a Unix timestamp.",
				"summarize": "UUIDv7 values are time ordered.",
			},
			wantEffects: []string{"plan", "work/0", "summarize"},
			wantAnswer:  "UUIDv7 values are time ordered.",
		},
		{
			name: "complex",
			plan: `{"kind":"complex","tasks":["compare storage choices","compare delivery choices"]}`,
			results: map[string]string{
				"work/0":    "Storage result.",
				"work/1":    "Delivery result.",
				"summarize": "Use the database for messages and Redis for delivery.",
			},
			wantEffects: []string{"plan", "work/0", "work/1", "summarize"},
			wantAnswer:  "Use the database for messages and Redis for delivery.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workCount := 0
			for _, effect := range test.wantEffects {
				if strings.HasPrefix(effect, "work/") {
					workCount++
				}
			}
			adapter := newScriptedAdapter(test.plan, test.results, workCount)
			server := newLoopServer(t, "task-1")
			runtime, err := loopruntime.New(server.URL, loopruntime.Options{
				HTTPClient: server.Client(), Harnesses: map[string]harness.Adapter{"temporary": adapter},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary", MaxSubtasks: 4})
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: objectKey("task-1"),
			}); err != nil {
				t.Fatal(err)
			}
			if got := adapter.effectKeys(); fmt.Sprint(got) != fmt.Sprint(test.wantEffects) {
				t.Fatalf("effects = %v, want %v", got, test.wantEffects)
			}
			answer, completed, failure := server.result()
			if answer != test.wantAnswer {
				t.Fatalf("answer = %q, want %q", answer, test.wantAnswer)
			}
			if !completed || failure != nil {
				t.Fatalf("completed = %t, failure = %#v", completed, failure)
			}
		})
	}
}

func TestReconcileCompletesInvalidPlanAsFailure(t *testing.T) {
	adapter := newScriptedAdapter(`{"kind":"complex","tasks":["only one"]}`, nil, 0)
	server := newLoopServer(t, "task-1")
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{
		HTTPClient: server.Client(), Harnesses: map[string]harness.Adapter{"temporary": adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: objectKey("task-1"),
	}); err != nil {
		t.Fatal(err)
	}
	_, completed, failure := server.result()
	if !completed || failure == nil || failure.Code != "router_failed" {
		t.Fatalf("completed = %t, failure = %#v", completed, failure)
	}
}

func (server *loopServer) result() (string, bool, *loopruntime.TaskFailure) {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.answer, server.completed, server.failure
}

type loopServer struct {
	*httptest.Server
	mu        sync.Mutex
	answer    string
	completed bool
	failure   *loopruntime.TaskFailure
}

func newLoopServer(t *testing.T, taskID string) *loopServer {
	t.Helper()
	value := &loopServer{}
	value.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/tasks/"+taskID:
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(loopd.TaskContext{
				ID:           taskID,
				Response:     loopd.Message{ID: "response", Purpose: "response"},
				Conversation: loopd.Conversation{ID: "conversation-1"},
				Input: loopd.Message{
					ID: "message-2", ConversationID: "conversation-1", TaskID: taskID,
					Kind: loopd.RoleUser, Key: "user-1", Content: semanticModel("How should this work?"),
				},
				History: []loopd.Message{
					{ID: "message-1", Kind: loopd.RoleOperator, Key: "router", Content: semanticModel("Earlier answer.")},
					{ID: "message-2", Kind: loopd.RoleUser, Key: "user-1", Content: semanticModel("How should this work?")},
				},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/tasks/"+taskID+"/outputs":
			var input loopd.OutputRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(response).Encode(loopd.Message{ID: input.Actor.Key, TaskID: taskID, Purpose: "output"})
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/events"):
			var input struct {
				Event json.RawMessage `json:"event"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			event, err := agentueui.Parse(input.Event)
			if err != nil {
				t.Error(err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			if event.Block["id"] == "answer" {
				value.mu.Lock()
				value.answer, _ = event.Block["content"].(string)
				value.mu.Unlock()
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(response, `{"id":%q}`, fmt.Sprintf("%d-0", event.Seq))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/tasks/"+taskID+"/complete":
			var input struct {
				Error *loopruntime.TaskFailure `json:"error"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			value.mu.Lock()
			value.completed = true
			value.failure = input.Error
			value.mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(value.Close)
	return value
}

type scriptedAdapter struct {
	mu        sync.Mutex
	plan      string
	results   map[string]string
	effects   []string
	wantWork  int
	workSeen  int
	workReady chan struct{}
	workOnce  sync.Once
}

func newScriptedAdapter(plan string, results map[string]string, wantWork int) *scriptedAdapter {
	return &scriptedAdapter{
		plan: plan, results: results, wantWork: wantWork, workReady: make(chan struct{}),
	}
}

func (adapter *scriptedAdapter) Prompt(_ context.Context, request harness.Request) (harness.Call, error) {
	effect := strings.TrimPrefix(request.IdempotencyKey, request.TaskID+"/")
	adapter.mu.Lock()
	adapter.effects = append(adapter.effects, effect)
	result := adapter.results[effect]
	if effect == "plan" {
		result = adapter.plan
	}
	if strings.HasPrefix(effect, "work/") {
		adapter.workSeen++
		if adapter.workSeen == adapter.wantWork {
			adapter.workOnce.Do(func() { close(adapter.workReady) })
		}
	}
	adapter.mu.Unlock()
	events := make(chan harness.Event)
	close(events)
	var waitFor <-chan struct{}
	if strings.HasPrefix(effect, "work/") {
		waitFor = adapter.workReady
	}
	return scriptedCall{id: request.CallID, events: events, result: result, waitFor: waitFor}, nil
}

func (adapter *scriptedAdapter) effectKeys() []string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return append([]string(nil), adapter.effects...)
}

type scriptedCall struct {
	id      string
	events  <-chan harness.Event
	result  string
	waitFor <-chan struct{}
}

func (call scriptedCall) ID() string                   { return call.id }
func (call scriptedCall) Events() <-chan harness.Event { return call.events }
func (call scriptedCall) Wait(ctx context.Context) (harness.Result, error) {
	if call.waitFor != nil {
		select {
		case <-ctx.Done():
			return harness.Result{}, ctx.Err()
		case <-call.waitFor:
		}
	}
	return harness.Result{Text: call.result}, nil
}

func semanticModel(text string) json.RawMessage {
	value, _ := json.Marshal(map[string]any{
		"version": "1.0", "biz": "chat", "meta": map[string]any{},
		"blocks": []map[string]any{{"id": "text", "type": "text", "content": text}},
	})
	return value
}

func objectKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name}
}
