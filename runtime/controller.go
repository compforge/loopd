package runtime

import (
	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConversationPredicate is controller setup support, not a collaboration Verb.
// Receipt updates do not enqueue work. On startup, uncommitted input is pending
// even if a prior process had already polled it.
func ConversationPredicate(actor loopd.ActorRef) predicate.Funcs {
	pending := func(value *conversationv1.Conversation) bool {
		return value.DeletionTimestamp == nil &&
			value.EndOffset(string(actor.Kind), actor.Key) > value.Committed(string(actor.Kind), actor.Key)
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
			return old.Wake(string(actor.Kind), actor.Key) != next.Wake(string(actor.Kind), actor.Key) ||
				old.EndOffset(string(actor.Kind), actor.Key) != next.EndOffset(string(actor.Kind), actor.Key) ||
				old.Committed(string(actor.Kind), actor.Key) != next.Committed(string(actor.Kind), actor.Key)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			value, ok := e.Object.(*conversationv1.Conversation)
			return ok && pending(value)
		},
		DeleteFunc: func(event.DeleteEvent) bool { return false },
	}
}
