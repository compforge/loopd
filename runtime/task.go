package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/task/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Task exposes loopd Task observation and context lookup to an Operator.
type Task struct {
	client *client
}

// Get resolves the chat state associated with a Task CRD. A Task may become
// visible to a controller just before the database transaction commits; in
// that case the server returns not found and the Reconciler should retry.
func (task Task) Get(ctx context.Context, taskID string) (loopd.TaskContext, error) {
	var result loopd.TaskContext
	err := task.client.do(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), nil, &result)
	return result, err
}

// Watch registers a controller-runtime Reconciler for Tasks routed to target.
// The Operator sees only a Task name and uses Get for current chat context.
func (task Task) Watch(mgr manager.Manager, target loopd.ResponderRef, reconciler reconcile.Reconciler) error {
	if !target.Valid() {
		return fmt.Errorf("invalid task target %q/%q", target.Kind, target.Key)
	}
	if err := taskv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("register loopd Task scheme: %w", err)
	}
	matchingTarget := predicate.NewPredicateFuncs(func(object controllerclient.Object) bool {
		value, ok := object.(*taskv1alpha1.Task)
		return ok && value.Spec.Target == target
	})
	return builder.ControllerManagedBy(mgr).
		Named("loopd-task").
		For(&taskv1alpha1.Task{}, builder.WithPredicates(matchingTarget)).
		Complete(reconciler)
}
