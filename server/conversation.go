package server

import (
	conversationclient "github.com/compforge/loopd/server/internal/conversation"
	"github.com/compforge/loopd/server/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

type ConversationCoordinator = service.ConversationCoordinator

// NewKubernetesConversationClient requires an uncached Kubernetes client.
func NewKubernetesConversationClient(kube client.Client, namespace string, timeout time.Duration) ConversationCoordinator {
	return conversationclient.NewClient(kube, namespace, timeout)
}
