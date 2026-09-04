package server

import (
	"context"
	"time"

	loopd "github.com/compforge/loopd"
	servertask "github.com/compforge/loopd/server/internal/task"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskMarker is the loop-server boundary for creating and compensating Task
// CRDs inside the chat submission work unit.
type TaskMarker interface {
	Create(context.Context, string, loopd.ResponderRef) error
	Delete(context.Context, string) error
}

// NewKubernetesTaskMarker creates the loop-server Task CRD adapter.
func NewKubernetesTaskMarker(kubeClient client.Client, namespace string, timeout time.Duration) TaskMarker {
	return servertask.NewKubernetesMarker(kubeClient, namespace, timeout)
}
