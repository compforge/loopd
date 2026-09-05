// Package v1alpha1 defines the Kubernetes API used by loopd to wake a selected
// Operator or Harness for one chat task.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const GroupName = "loopd.compforge.io"

var (
	GroupVersion  = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &Conversation{}, &ConversationList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
