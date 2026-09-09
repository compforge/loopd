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
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	conversationv1 "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	loopruntime "github.com/compforge/loopd/runtime"
	"github.com/compforge/loopd/server/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
				HTTPClient: testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary", MaxSubtasks: 4})
			if reconciler != nil {
				reconciler.reader = routerReader(t)
			}
			if err != nil {
				t.Fatal(err)
			}

			// Plan, work and summary each observe completion on the runtime's
			// status polling interval; the test budget covers all three stages.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: objectKey("conversation-1"),
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
		HTTPClient: testutil.WithHarnesses(t, server.Client(), map[string]harness.Adapter{"temporary": adapter}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if reconciler != nil {
		reconciler.reader = routerReader(t)
	}
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: objectKey("conversation-1"),
	}); err == nil {
		t.Fatal("invalid plan must report execution failure")
	}
	_, completed, failure := server.result()
	if !completed || failure == nil || failure.Code != "router_failed" {
		t.Fatalf("completed = %t, failure = %#v", completed, failure)
	}
}

func (server *loopServer) result() (string, bool, *testFailure) {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.answer, server.completed, server.failure
}

type testFailure struct {
	Code    string
	Message string
}

type loopServer struct {
	*httptest.Server
	mu            sync.Mutex
	answer        string
	completed     bool
	failure       *testFailure
	failureStatus contract.MessageStatus
	polls         int
	inbox         [][]contract.Message
	completedIDs  []string
}

func newLoopServer(t *testing.T, taskID string) *loopServer {
	t.Helper()
	value := &loopServer{}
	value.Server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/commit"):
			var input contract.CommitRequest
			_ = json.NewDecoder(request.Body).Decode(&input)
			value.mu.Lock()
			if value.failure != nil && value.failureStatus != contract.MessageStatusFailed {
				t.Error("failure message must have failed status before Commit")
			}
			value.completed = true
			value.completedIDs = append(value.completedIDs, input.Through)
			value.mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/messages"):
			var input contract.SpeakRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Actor.Kind == contract.ActorKindHarness && request.URL.Path != "/v1/conversations/workspace-1/messages" {
				t.Errorf("Harness output did not use projected conversation: %s", request.URL.Path)
			}
			var content struct {
				Meta   struct{ Error *testFailure }
				Blocks []struct {
					ID      string
					Content string
				}
			}
			if err := json.Unmarshal(input.Content, &content); err != nil {
				t.Error(err)
			}
			if content.Meta.Error != nil {
				value.mu.Lock()
				value.failure = content.Meta.Error
				value.failureStatus = input.Status
				value.mu.Unlock()
			}
			for _, block := range content.Blocks {
				if block.ID == "answer" {
					value.mu.Lock()
					value.answer = block.Content
					value.mu.Unlock()
				}
			}
			_ = json.NewEncoder(response).Encode(contract.Message{ID: input.Key, Content: input.Content, Status: input.Status})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/conversations/conversation-1/poll":
			value.mu.Lock()
			var messages []contract.Message
			if value.polls == 0 {
				messages = []contract.Message{{ID: "message-2", ConversationID: "conversation-1", TaskID: taskID, SourceKind: contract.ActorKindUser, SourceKey: "user-1", Content: semanticModel("How should this work?")}}
			} else if len(value.inbox) > 0 {
				messages = value.inbox[0]
				value.inbox = value.inbox[1:]
			}
			value.polls++
			value.mu.Unlock()
			position := ""
			if len(messages) > 0 {
				position = messages[len(messages)-1].ID
			}
			_ = json.NewEncoder(response).Encode(contract.PollResult{Messages: messages, Position: position})
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/content"):
			id := strings.Split(request.URL.Path, "/")[5]
			_ = json.NewEncoder(response).Encode(contract.Message{ID: id, SourceKind: contract.ActorKindOperator, SourceKey: "router", Content: semanticModel("Earlier answer.")})
		case request.Method == http.MethodGet && request.URL.Path == "/v1/conversations/conversation-1/messages":
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"data": []contract.Message{
					{ID: "message-1", SourceKind: contract.ActorKindOperator, SourceKey: "router", Content: semanticModel("Earlier answer.")},
				},
			})
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/events"):
			var input struct {
				Event  json.RawMessage        `json:"event"`
				Status contract.MessageStatus `json:"status"`
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
			if event.Op == agentueui.OpEnd && input.Status == contract.MessageStatusFailed {
				value.mu.Lock()
				value.failureStatus = input.Status
				value.mu.Unlock()
			}
			if event.Block["id"] == "answer" {
				value.mu.Lock()
				value.answer, _ = event.Block["content"].(string)
				value.mu.Unlock()
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(response, `{"id":%q}`, fmt.Sprintf("%d-0", event.Seq))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(value.Close)
	return value
}

type scriptedAdapter struct {
	mu          sync.Mutex
	plan        string
	results     map[string]string
	effects     []string
	wantWork    int
	workSeen    int
	workReady   chan struct{}
	workOnce    sync.Once
	holdWork    <-chan struct{}
	started     chan struct{}
	startedOnce sync.Once
	prompts     map[string]string
}

func newScriptedAdapter(plan string, results map[string]string, wantWork int) *scriptedAdapter {
	return &scriptedAdapter{
		plan: plan, results: results, wantWork: wantWork, workReady: make(chan struct{}), started: make(chan struct{}), prompts: make(map[string]string),
	}
}

func (adapter *scriptedAdapter) Prompt(_ context.Context, request harness.Request) (harness.Call, error) {
	effect := request.EffectKey
	adapter.mu.Lock()
	adapter.effects = append(adapter.effects, effect)
	adapter.prompts[effect] = request.Prompt
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
		if adapter.holdWork != nil {
			waitFor = adapter.holdWork
		}
		adapter.startedOnce.Do(func() { close(adapter.started) })
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

func routerReader(t *testing.T) client.Reader {
	t.Helper()
	scheme := kuberuntime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(&conversationv1.Conversation{
		ObjectMeta: metav1.ObjectMeta{Name: "conversation-1"},
		Spec:       conversationv1.ConversationSpec{Participants: []conversationv1.ConversationParticipant{{Kind: routerActor.Kind, Key: routerActor.Key, ConversationID: "workspace-1"}}},
	}).Build()
}

func TestReconcileWaitsForProjectedDetail(t *testing.T) {
	server := newLoopServer(t, "task")
	runtime, err := loopruntime.New(server.URL, loopruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	reconciler, err := New(runtime.Loop, Config{HarnessTarget: "temporary"})
	if err != nil {
		t.Fatal(err)
	}
	kube := routerReader(t).(client.Client)
	var conv conversationv1.Conversation
	key := types.NamespacedName{Name: "conversation-1"}
	if err := kube.Get(context.Background(), key, &conv); err != nil {
		t.Fatal(err)
	}
	conv.Spec.Participants[0].ConversationID = ""
	if err := kube.Update(context.Background(), &conv); err != nil {
		t.Fatal(err)
	}
	reconciler.reader = kube
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err == nil {
		t.Fatal("missing binding must retry")
	}
	if server.polls != 0 || server.completed {
		t.Fatal("missing binding consumed input")
	}
}
