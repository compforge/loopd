// Package task implements loop-server's Kubernetes client for Task CRDs.
package task

import (
	"context"
	"fmt"
	"time"

	loopd "github.com/compforge/loopd"
	taskv1alpha1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultOperationTimeout = 10 * time.Second

// Client persists loopd Task CRDs through a controller-runtime client.
type Client struct {
	kubeClient controllerclient.Client
	namespace  string
	timeout    time.Duration
}

// NewClient creates a Task client scoped to one namespace.
func NewClient(kubeClient controllerclient.Client, namespace string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultOperationTimeout
	}
	return &Client{kubeClient: kubeClient, namespace: namespace, timeout: timeout}
}

func (client *Client) Create(ctx context.Context, taskID string, target loopd.ActorRef) error {
	ctx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	value := &taskv1alpha1.Task{
		TypeMeta: metav1.TypeMeta{
			APIVersion: taskv1alpha1.GroupVersion.String(),
			Kind:       taskv1alpha1.TaskKind,
		},
		ObjectMeta: metav1.ObjectMeta{Name: taskID, Namespace: client.namespace},
		Spec: taskv1alpha1.TaskSpec{
			Target: taskv1alpha1.TaskTarget{
				Kind: taskv1alpha1.TaskTargetKind(target.Kind),
				Key:  target.Key,
			},
			Revision: 1,
		},
	}
	if err := client.kubeClient.Create(ctx, value); err != nil {
		return fmt.Errorf("create Task %q: %w", taskID, err)
	}
	return nil
}

func (client *Client) Delete(ctx context.Context, taskID string) error {
	ctx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	value := &taskv1alpha1.Task{ObjectMeta: metav1.ObjectMeta{Name: taskID, Namespace: client.namespace}}
	if err := controllerclient.IgnoreNotFound(client.kubeClient.Delete(ctx, value)); err != nil {
		return fmt.Errorf("delete Task %q: %w", taskID, err)
	}
	return nil
}
