package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestTaskMessagesReadsVisiblePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/tasks/task%2F1/messages" || r.URL.Query().Get("after") != "message/1" || r.URL.Query().Get("limit") != "2" {
			t.Errorf("unexpected read request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(page[loopd.Message]{Data: []loopd.Message{
			{ID: "m2", TaskID: "task/1", ConversationID: "root", Kind: loopd.RoleUser, Key: "alice"},
			{ID: "m3", TaskID: "task/1", ConversationID: "work", Kind: loopd.RoleHarness, Key: "call"},
		}})
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	messages, err := runtime.Loop.Task.Messages(context.Background(), "task/1", "message/1", 2)
	if err != nil || len(messages) != 2 || messages[0].ConversationID != "root" || messages[1].ConversationID != "work" {
		t.Fatalf("task messages = %+v, error = %v", messages, err)
	}
}

func TestTaskPredicateRoutesCreatesAndIgnoresCompletionDelete(t *testing.T) {
	target := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}
	matching := &taskv1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "task-1"},
		Spec: taskv1alpha1.TaskSpec{Target: taskv1alpha1.TaskTarget{
			Kind: taskv1alpha1.TaskTargetOperator, Key: "router",
		}},
	}
	other := matching.DeepCopy()
	other.Spec.Target.Key = "other"
	predicate := taskPredicate(target)

	if !predicate.Create(event.CreateEvent{Object: matching}) {
		t.Fatal("matching Task create was filtered")
	}
	if predicate.Create(event.CreateEvent{Object: other}) {
		t.Fatal("other Task create was accepted")
	}
	if predicate.Delete(event.DeleteEvent{Object: matching}) {
		t.Fatal("Task completion delete was accepted")
	}
}
