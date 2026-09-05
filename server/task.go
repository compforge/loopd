package server

import (
	"context"
	"time"

	loopd "github.com/compforge/loopd"
	servertask "github.com/compforge/loopd/server/internal/task"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TaskClient is the loop-server boundary for managing marker CRDs across one
// chat task's active lifetime.
type TaskClient interface {
	Create(context.Context, string, loopd.ActorRef) error
	Delete(context.Context, string) error
	Wake(context.Context, string) error
	Exists(context.Context, string) (bool, error)
}

// NewKubernetesTaskClient creates the loop-server Task CRD client.
func NewKubernetesTaskClient(kubeClient client.Client, namespace string, timeout time.Duration) TaskClient {
	return servertask.NewClient(kubeClient, namespace, timeout)
}
