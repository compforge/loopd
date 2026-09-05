package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestConversationWatchOnlyWakesAddressedActor(t *testing.T) {
	predicate := ConversationPredicate(loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"})
	original := &conversationv1.Conversation{
		Spec: conversationv1.ConversationSpec{Participants: []conversationv1.ConversationParticipant{
			{Kind: "operator", Key: "a", EndOffset: "001"},
			{Kind: "operator", Key: "b", EndOffset: "002"},
		}},
	}
	if !predicate.Create(event.CreateEvent{Object: original}) {
		t.Fatal("unreceived input must wake A on startup")
	}
	other := original.DeepCopy()
	other.Spec.Participants[1].EndOffset = "003"
	if predicate.Update(event.UpdateEvent{ObjectOld: original, ObjectNew: other}) {
		t.Fatal("B's signal woke A")
	}
	addressed := other.DeepCopy()
	addressed.Spec.Participants[0].EndOffset = "004"
	if !predicate.Update(event.UpdateEvent{ObjectOld: other, ObjectNew: addressed}) {
		t.Fatal("A's signal did not wake A")
	}
	partial := addressed.DeepCopy()
	partial.Status.Consumers = []conversationv1.ConversationConsumer{{Kind: "operator", Key: "a", Committed: "001"}}
	if !predicate.Update(event.UpdateEvent{ObjectOld: addressed, ObjectNew: partial}) {
		t.Fatal("partial batch should continue draining")
	}
	drained := partial.DeepCopy()
	drained.Status.Consumers[0].Committed = "004"
	if predicate.Update(event.UpdateEvent{ObjectOld: partial, ObjectNew: drained}) {
		t.Fatal("completed receipt should not loop on itself")
	}
	if predicate.Delete(event.DeleteEvent{Object: drained}) {
		t.Fatal("deletion should not dispatch work")
	}
}

func TestConversationReadAndPollUseDifferentVerbs(t *testing.T) {
	var read, poll int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/conversations/conv/messages":
			read++
			if r.URL.Query().Get("after") != "001" {
				t.Error("Read omitted caller's history position")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []loopd.Message{{ID: "002"}}})
		case "POST /v1/conversations/conv/poll":
			poll++
			var request loopd.PollRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Actor.Key != "a" || request.Limit != 10 {
				t.Errorf("Poll request = %+v", request)
			}
			_ = json.NewEncoder(w).Encode(loopd.PollResult{Messages: []loopd.Message{{ID: "003"}}, Position: "003"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runtime, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	messages, err := runtime.Loop.Conv.Read(context.Background(), "conv", "001", 10)
	if err != nil || len(messages) != 1 || messages[0].ID != "002" {
		t.Fatalf("Read = %+v, %v", messages, err)
	}
	result, err := runtime.Loop.Conv.Poll(context.Background(), "conv", loopd.PollRequest{
		Actor: loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"}, Limit: 10,
	})
	if err != nil || result.Position != "003" {
		t.Fatalf("Poll = %+v, %v", result, err)
	}
	if read != 1 || poll != 1 {
		t.Fatalf("read = %d, poll = %d", read, poll)
	}
}
