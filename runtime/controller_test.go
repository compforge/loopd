package runtime

import (
	loopd "github.com/compforge/loopd"
	convapi "github.com/compforge/loopd/runtime/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"testing"
)

func TestConversationPredicateUsesUncommittedInputNotReceipt(t *testing.T) {
	actor := loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "a"}
	predicate := ConversationPredicate(actor)
	old := &convapi.Conversation{
		Spec:   convapi.ConversationSpec{Participants: []convapi.ConversationParticipant{{Kind: "operator", Key: "a", EndOffset: "003"}}},
		Status: convapi.ConversationStatus{Consumers: []convapi.ConversationConsumer{{Kind: "operator", Key: "a", Committed: "001", Position: "001"}}},
	}
	received := old.DeepCopy()
	received.Status.Consumers[0].Position = "003"
	if predicate.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: received}) {
		t.Fatal("receipt must not wake itself")
	}
	if !predicate.Create(event.CreateEvent{Object: received}) {
		t.Fatal("restart must wake uncommitted input")
	}
	committed := received.DeepCopy()
	committed.Status.Consumers[0].Committed = "002"
	if !predicate.Update(event.UpdateEvent{ObjectOld: received, ObjectNew: committed}) {
		t.Fatal("remaining backlog must wake")
	}
	drained := committed.DeepCopy()
	drained.Status.Consumers[0].Committed = "003"
	if predicate.Update(event.UpdateEvent{ObjectOld: committed, ObjectNew: drained}) {
		t.Fatal("drained inbox must not spin")
	}
	other := received.DeepCopy()
	other.Spec.Participants = append(other.Spec.Participants, convapi.ConversationParticipant{Kind: "operator", Key: "b", EndOffset: "004"})
	if predicate.Update(event.UpdateEvent{ObjectOld: received, ObjectNew: other}) {
		t.Fatal("another actor's signal must not wake a")
	}
}
