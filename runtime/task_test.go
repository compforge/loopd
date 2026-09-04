package runtime

import (
	"testing"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

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
