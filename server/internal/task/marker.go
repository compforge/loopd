// Package task implements loop-server's Task CRD persistence adapter.
package task

import (
	"context"
	"fmt"
	"time"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultOperationTimeout = 10 * time.Second

// Marker creates and removes the durable Kubernetes wake-up marker associated
// with one chat task.
type Marker interface {
	Create(context.Context, string, loopd.ResponderRef) error
	Delete(context.Context, string) error
}

// KubernetesMarker persists Task markers through a controller-runtime client.
type KubernetesMarker struct {
	client    client.Client
	namespace string
	timeout   time.Duration
}

// NewKubernetesMarker creates a marker client scoped to one namespace.
func NewKubernetesMarker(kubeClient client.Client, namespace string, timeout time.Duration) *KubernetesMarker {
	if timeout <= 0 {
		timeout = defaultOperationTimeout
	}
	return &KubernetesMarker{client: kubeClient, namespace: namespace, timeout: timeout}
}

func (marker *KubernetesMarker) Create(ctx context.Context, taskID string, target loopd.ResponderRef) error {
	ctx, cancel := context.WithTimeout(ctx, marker.timeout)
	defer cancel()

	value := &taskv1alpha1.Task{
		TypeMeta: metav1.TypeMeta{
			APIVersion: taskv1alpha1.GroupVersion.String(),
			Kind:       taskv1alpha1.TaskKind,
		},
		ObjectMeta: metav1.ObjectMeta{Name: taskID, Namespace: marker.namespace},
		Spec: taskv1alpha1.TaskSpec{
			Target:   target,
			Revision: 1,
		},
	}
	if err := marker.client.Create(ctx, value); err != nil {
		return fmt.Errorf("create Task %q: %w", taskID, err)
	}
	return nil
}

func (marker *KubernetesMarker) Delete(ctx context.Context, taskID string) error {
	ctx, cancel := context.WithTimeout(ctx, marker.timeout)
	defer cancel()

	value := &taskv1alpha1.Task{ObjectMeta: metav1.ObjectMeta{Name: taskID, Namespace: marker.namespace}}
	if err := client.IgnoreNotFound(marker.client.Delete(ctx, value)); err != nil {
		return fmt.Errorf("delete Task %q: %w", taskID, err)
	}
	return nil
}
