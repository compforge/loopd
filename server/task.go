package server

import (
	"context"
	"time"

	loopd "github.com/compforge/loopd"
	servertask "github.com/compforge/loopd/server/internal/task"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskClient is the loop-server boundary for managing Task CRDs inside the
// chat submission work unit.
type TaskClient interface {
	Create(context.Context, string, loopd.ResponderRef) error
	Delete(context.Context, string) error
}

// NewKubernetesTaskClient creates the loop-server Task CRD client.
func NewKubernetesTaskClient(kubeClient client.Client, namespace string, timeout time.Duration) TaskClient {
	return servertask.NewClient(kubeClient, namespace, timeout)
}
