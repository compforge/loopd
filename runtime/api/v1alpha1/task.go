package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const TaskKind = "Task"

// +kubebuilder:validation:Enum=operator;harness
type TaskTargetKind string

const (
	TaskTargetOperator TaskTargetKind = "operator"
	TaskTargetHarness  TaskTargetKind = "harness"
)

type TaskTarget struct {
	// +required
	Kind TaskTargetKind `json:"kind"`
	// +required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// TaskSpec currently contains the minimum information needed to dispatch one
// chat task. New generic coordination fields can evolve this API additively;
// chat content and Operator-owned domain state remain outside this resource.
type TaskSpec struct {
	// +required
	Target TaskTarget `json:"target"`
	// +required
	// +kubebuilder:validation:Minimum=1
	Revision int64 `json:"revision"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced

// Task is loopd's durable dispatch resource for one chat task.
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
