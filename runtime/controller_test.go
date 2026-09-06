package runtime

import (
	"github.com/compforge/loopd/pkg/contract"
	convapi "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"testing"
)

func TestConversationPredicateUsesUncommittedInputNotReceipt(t *testing.T) {
	actor := contract.ActorRef{Kind: contract.ActorKindOperator, Key: "a"}
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

func TestParticipantJoinsFullActorIdentityAndRepairsWake(t *testing.T) {
	actor := contract.ActorRef{Kind: "operator/custom/manager", Key: "same"}
	conv := &convapi.Conversation{
		Spec:   convapi.ConversationSpec{Participants: []convapi.ConversationParticipant{{Kind: "operator", Key: "same", ConversationID: "other"}, {Kind: actor.Kind, Key: actor.Key, EndOffset: "003"}}},
		Status: convapi.ConversationStatus{Consumers: []convapi.ConversationConsumer{{Kind: "operator", Key: "same", Committed: "999"}, {Kind: actor.Kind, Key: actor.Key, Position: "003", Committed: "001"}}},
	}
	state, ok := Participant(conv, actor)
	if !ok || state.Actor != actor || state.ConversationID != "" || state.EndOffset != "003" || state.Position != "003" || state.Committed != "001" {
		t.Fatalf("merged state: %+v %v", state, ok)
	}
	repaired := conv.DeepCopy()
	repaired.Spec.Participants[1].ConversationID = "detail"
	if !ConversationPredicate(actor).Update(event.UpdateEvent{ObjectOld: conv, ObjectNew: repaired}) {
		t.Fatal("binding repair must wake pending work")
	}
	if ConversationPredicate(contract.ActorRef{Kind: "operator", Key: "same"}).Update(event.UpdateEvent{ObjectOld: conv, ObjectNew: repaired}) {
		t.Fatal("binding repair woke other actor")
	}
	if _, ok := Participant(conv, contract.ActorRef{Kind: actor.Kind, Key: "missing"}); ok {
		t.Fatal("unknown member")
	}
	if _, ok := Participant(nil, actor); ok {
		t.Fatal("nil member")
	}
}
