package v1alpha1

import (
	loopd "github.com/compforge/loopd"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const TaskKind = "Task"

// TaskSpec intentionally contains only dispatch information. Query content,
// conversation history, and execution details remain in loop-server and are
// resolved through loop-runtime by using the Task name as the task ID.
type TaskSpec struct {
	Target   loopd.ResponderRef `json:"target"`
	Revision int64              `json:"revision"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced

// Task is loopd's durable marker for routing and waking one chat task.
type Task struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TaskSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// TaskList contains loopd Task resources.
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Task `json:"items"`
}

func (task *Task) DeepCopyInto(out *Task) {
	*out = *task
	task.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
}

func (task *Task) DeepCopy() *Task {
	if task == nil {
		return nil
	}
	out := new(Task)
	task.DeepCopyInto(out)
	return out
}

func (task *Task) DeepCopyObject() runtime.Object { return task.DeepCopy() }

func (list *TaskList) DeepCopyInto(out *TaskList) {
	*out = *list
	list.ListMeta.DeepCopyInto(&out.ListMeta)
	if list.Items != nil {
		out.Items = make([]Task, len(list.Items))
		for index := range list.Items {
			list.Items[index].DeepCopyInto(&out.Items[index])
		}
	}
}

func (list *TaskList) DeepCopy() *TaskList {
	if list == nil {
		return nil
	}
	out := new(TaskList)
	list.DeepCopyInto(out)
	return out
}

func (list *TaskList) DeepCopyObject() runtime.Object { return list.DeepCopy() }
