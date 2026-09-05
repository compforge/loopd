package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Conv exposes the persistent collaboration boundary to Operators.
type Conv struct{ client *client }

// Listen is a write Verb: receive a bounded batch and advance this participant's
// CRD cursor. It does not wait for new input. Receipt is not a business checkpoint;
// a request whose response is lost may already have advanced the cursor.
func (conv Conv) Listen(ctx context.Context, conversationID string, request loopd.ListenRequest) (loopd.ListenResult, error) {
	var result loopd.ListenResult
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/listen", request, &result)
	return result, err
}

// Read is a read Verb. Unlike Listen, reading shared history never acknowledges
// input or implicitly schedules another participant.
func (conv Conv) Read(ctx context.Context, conversationID, after string, limit int) ([]loopd.Message, error) {
	return (Chat{client: conv.client}).History(ctx, conversationID, after, limit)
}

type ConvWatchOptions struct{ MaxConcurrentReconciles int }

// Watch wakes only the addressed actor. Call Listen again to drain a full batch;
// controller-runtime may coalesce several signals into one reconciliation.
func (conv Conv) Watch(mgr manager.Manager, actor loopd.ActorRef, reconciler reconcile.Reconciler, options ConvWatchOptions) error {
	if !actor.ValidTarget() {
		return fmt.Errorf("invalid conversation actor %q/%q", actor.Kind, actor.Key)
	}
	if err := conversationv1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	return builder.ControllerManagedBy(mgr).
		Named("loopd-conversation").
		For(&conversationv1.Conversation{}, builder.WithPredicates(conversationPredicate(actor))).
		WithOptions(controller.Options{MaxConcurrentReconciles: options.MaxConcurrentReconciles}).
		Complete(reconciler)
}

func conversationPredicate(actor loopd.ActorRef) predicate.Funcs {
	pending := func(value *conversationv1.Conversation) bool {
		return value.DeletionTimestamp == nil &&
			value.LatestMessageID(string(actor.Kind), actor.Key) > value.LastMessageID(string(actor.Kind), actor.Key)
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
			return old.LatestMessageID(string(actor.Kind), actor.Key) != next.LatestMessageID(string(actor.Kind), actor.Key) ||
				old.LastMessageID(string(actor.Kind), actor.Key) != next.LastMessageID(string(actor.Kind), actor.Key)
		},
		GenericFunc: func(e event.GenericEvent) bool {
			value, ok := e.Object.(*conversationv1.Conversation)
			return ok && pending(value)
		},
		DeleteFunc: func(event.DeleteEvent) bool { return false },
	}
}
