package runtime

import (
	"github.com/compforge/loopd/pkg/contract"
	conversationv1 "github.com/compforge/loopd/pkg/k8s/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConversationPredicate is controller setup support, not a collaboration Verb.
// Receipt updates do not enqueue work. On startup, uncommitted input is pending
// even if a prior process had already polled it.
func ConversationPredicate(actor contract.ActorRef) predicate.Funcs {
	pending := func(value *conversationv1.Conversation) bool {
		return value.DeletionTimestamp == nil &&
			value.EndOffset(actor.Kind, actor.Key) > value.Committed(actor.Kind, actor.Key)
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			value, ok := e.Object.(*conversationv1.Conversation)
			return ok && pending(value)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			old, oldOK := e.ObjectOld.(*conversationv1.Conversation)
			next, nextOK := e.ObjectNew.(*conversationv1.Conversation)
			if !oldOK || !nextOK || !pending(next) {
				return false
			}
			// Changes to B's signal/status do not wake A. Advancing A's cursor
			// with unread input remaining does wake A to drain the next batch.
			previous, _ := Participant(old, actor)
			current, _ := Participant(next, actor)
			return previous.ConversationID != current.ConversationID || old.Wake(actor.Kind, actor.Key) != next.Wake(actor.Kind, actor.Key) ||
				old.EndOffset(actor.Kind, actor.Key) != next.EndOffset(actor.Kind, actor.Key) ||
				old.Committed(actor.Kind, actor.Key) != next.Committed(actor.Kind, actor.Key)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			value, ok := e.Object.(*conversationv1.Conversation)
			return ok && pending(value)
		},
		DeleteFunc: func(event.DeleteEvent) bool { return false },
	}
}

// ParticipantState joins a participant's server projection and consumption
// status without changing the CRD's spec/status ownership or making a request.
type ParticipantState struct {
	Actor          contract.ActorRef
	ConversationID string
	EndOffset      string
	Position       string
	Committed      string
}

// Participant reads one actor's local Conv snapshot. ActorKind is open; both
// kind and key identify the participant. A status-only entry is not a member.
func Participant(conv *conversationv1.Conversation, actor contract.ActorRef) (ParticipantState, bool) {
	if conv == nil {
		return ParticipantState{}, false
	}
	for _, p := range conv.Spec.Participants {
		if p.Kind != actor.Kind || p.Key != actor.Key {
			continue
		}
		state := ParticipantState{Actor: actor, ConversationID: p.ConversationID, EndOffset: p.EndOffset}
		for _, c := range conv.Status.Consumers {
			if c.Kind == actor.Kind && c.Key == actor.Key {
				state.Position, state.Committed = c.Position, c.Committed
				break
			}
		}
		return state, true
	}
	return ParticipantState{}, false
}
