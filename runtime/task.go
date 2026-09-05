package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Task exposes loopd Task observation and context lookup to an Operator.
type Task struct {
	client *client
}

// Messages is a read Effect returning a page of visible messages across the Task's user and work
// conversations, including Human questions and replies. Each result retains its
// conversation ID. Use Chat.History to read just one conversation across tasks.
func (task Task) Messages(ctx context.Context, taskID, after string, limit int) ([]loopd.Message, error) {
	var result page[loopd.Message]
	path := "/v1/tasks/" + url.PathEscape(taskID) + "/messages?after=" + url.QueryEscape(after) + "&limit=" + strconv.Itoa(limit)
	err := task.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

// TaskWatchOptions configures the controller that observes one Task target.
type TaskWatchOptions struct {
	MaxConcurrentReconciles int
}

// Get is a read Effect resolving the initial chat state associated with a Task CRD. A Task may become
// visible to a controller just before the database transaction commits; in
// that case the server returns not found and the Reconciler should retry.
func (task Task) Get(ctx context.Context, taskID string) (loopd.TaskContext, error) {
	var result loopd.TaskContext
	err := task.client.do(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), nil, &result)
	return result, err
}

// Watch is a read Effect subscribing a controller-runtime Reconciler to Tasks routed to target.
// The Operator sees only a Task name and uses Get for current chat context.
func (task Task) Watch(
	mgr manager.Manager,
	target loopd.ActorRef,
	reconciler reconcile.Reconciler,
	options ...TaskWatchOptions,
) error {
	if !target.ValidTarget() {
		return fmt.Errorf("invalid task target %q/%q", target.Kind, target.Key)
	}
	if len(options) > 1 {
		return errors.New("Task Watch accepts at most one options value")
	}
	if err := taskv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("register loopd Task scheme: %w", err)
	}
	controllerOptions := controller.Options{}
	if len(options) == 1 {
		controllerOptions.MaxConcurrentReconciles = options[0].MaxConcurrentReconciles
	}
	return builder.ControllerManagedBy(mgr).
		Named("loopd-task").
		For(&taskv1alpha1.Task{}, builder.WithPredicates(taskPredicate(target))).
		WithOptions(controllerOptions).
		Complete(reconciler)
}

func taskPredicate(target loopd.ActorRef) predicate.Funcs {
	matches := func(object controllerclient.Object) bool {
		value, ok := object.(*taskv1alpha1.Task)
		return ok && value.Spec.Target.Kind == taskv1alpha1.TaskTargetKind(target.Kind) &&
			value.Spec.Target.Key == target.Key
	}
	return predicate.Funcs{
		CreateFunc:  func(value event.CreateEvent) bool { return matches(value.Object) },
		UpdateFunc:  func(value event.UpdateEvent) bool { return matches(value.ObjectNew) },
		GenericFunc: func(value event.GenericEvent) bool { return matches(value.Object) },
		// Completion deletes the marker. Enqueuing that delete would run the
		// already completed chat task again because its Messages remain durable.
		DeleteFunc: func(event.DeleteEvent) bool { return false },
	}
}
