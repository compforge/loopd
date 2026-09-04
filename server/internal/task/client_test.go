package task

import (
	"context"
	"testing"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClientLifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := taskv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	tasks := NewClient(kubeClient, "loopd-system", 0)
	target := loopd.ResponderRef{Kind: loopd.RoleOperator, Key: "operator-1"}

	if err := tasks.Create(context.Background(), "01991f3d-1110-7000-8000-000000000000", target); err != nil {
		t.Fatal(err)
	}
	var created taskv1alpha1.Task
	key := client.ObjectKey{Namespace: "loopd-system", Name: "01991f3d-1110-7000-8000-000000000000"}
	if err := kubeClient.Get(context.Background(), key, &created); err != nil {
		t.Fatal(err)
	}
	if created.Spec.Target.Kind != taskv1alpha1.TaskTargetKind(target.Kind) ||
		created.Spec.Target.Key != target.Key || created.Spec.Revision != 1 {
		t.Fatalf("created Task = %#v", created.Spec)
	}
	if err := tasks.Delete(context.Background(), created.Name); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), key, &created); client.IgnoreNotFound(err) != nil || err == nil {
		t.Fatalf("Task still exists or lookup failed: %v", err)
	}
}
