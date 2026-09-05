// Package conversation owns the Kubernetes side of conversation delivery.
package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"

	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrNotParticipant = errors.New("actor is not a conversation participant")

type Client struct {
	kube      client.Client
	namespace string
	timeout   time.Duration
}

// NewClient requires a direct (uncached) Kubernetes client. Listen's conflict
// retries must observe the latest cursor, not a controller cache's old version.
func NewClient(kube client.Client, namespace string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{kube: kube, namespace: namespace, timeout: timeout}
}

// Signal records a committed message's recipient. An empty target is an
// explicit broadcast to existing participants; it never registers all Operators.
func (c *Client) Signal(ctx context.Context, conversationID, messageID string, target loopd.ActorRef) error {
	if conversationID == "" || messageID == "" ||
		(target != (loopd.ActorRef{}) && !target.ValidTarget()) {
		return errors.New("conversation, message and a valid target or broadcast are required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return retry.OnError(retry.DefaultRetry, func(err error) bool {
		return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)
	}, func() error {
		value := &conversationv1.Conversation{}
		err := c.kube.Get(ctx, client.ObjectKey{Name: conversationID, Namespace: c.namespace}, value)
		create := apierrors.IsNotFound(err)
		if err != nil && !create {
			return err
		}
		if create {
			value = &conversationv1.Conversation{
				ObjectMeta: metav1.ObjectMeta{Name: conversationID, Namespace: c.namespace},
			}
		}
		found, changed := false, false
		for i := range value.Spec.Participants {
			participant := &value.Spec.Participants[i]
			if target == (loopd.ActorRef{}) || (participant.Kind == string(target.Kind) && participant.Key == target.Key) {
				found = true
				if participant.LatestMessageID < messageID {
					participant.LatestMessageID, changed = messageID, true
				}
			}
		}
		if !found && target != (loopd.ActorRef{}) {
			value.Spec.Participants = append(value.Spec.Participants, conversationv1.ConversationParticipant{
				Kind: string(target.Kind), Key: target.Key, LatestMessageID: messageID,
			})
			changed = true
		}
		if create {
			return c.kube.Create(ctx, value)
		}
		if !changed {
			return nil
		}
		return c.kube.Update(ctx, value)
	})
}

// ReadMessages queries SQL for messages addressed to actor (or broadcasts)
// after the receipt cursor, ordered by UUIDv7 ID, with a bounded batch size.
// The CRD wake signal is not a query bound or a substitute for this read.
type ReadMessages = func(ctx context.Context, after string) ([]loopd.Message, error)

// Listen advances receipt after reading a nonempty batch. A lost HTTP response
// after the status update can lose that delivery; this is NOT a business
// checkpoint or exactly-once execution. History remains available through Read.
// +spec=`Listen 是 write verb；各参与者游标独立，只确认接收，不表示业务完成`
func (c *Client) Listen(ctx context.Context, conversationID string, actor loopd.ActorRef, read ReadMessages) (loopd.ListenResult, error) {
	if conversationID == "" || !actor.ValidTarget() || read == nil {
		return loopd.ListenResult{}, errors.New("conversation, actor and message reader are required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var result loopd.ListenResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		result = loopd.ListenResult{Messages: []loopd.Message{}}
		value := &conversationv1.Conversation{}
		if err := c.kube.Get(ctx, client.ObjectKey{Name: conversationID, Namespace: c.namespace}, value); err != nil {
			return err
		}
		participant := false
		for _, entry := range value.Spec.Participants {
			if entry.Kind == string(actor.Kind) && entry.Key == actor.Key {
				participant = true
				break
			}
		}
		if !participant {
			return ErrNotParticipant
		}
		after := value.LastMessageID(string(actor.Kind), actor.Key)
		result.LastMessageID = after
		messages, err := read(ctx, after)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		next := after
		for _, message := range messages {
			if message.ConversationID != conversationID || message.ID <= next ||
				!addressedTo(message, actor) {
				return fmt.Errorf("invalid Listen batch for conversation %q", conversationID)
			}
			next = message.ID
		}
		found := false
		for i := range value.Status.Listeners {
			listener := &value.Status.Listeners[i]
			if listener.Kind == string(actor.Kind) && listener.Key == actor.Key {
				listener.LastMessageID, found = next, true
				break
			}
		}
		if !found {
			value.Status.Listeners = append(value.Status.Listeners, conversationv1.ConversationListener{
				Kind: string(actor.Kind), Key: actor.Key, LastMessageID: next,
			})
		}
		if err := c.kube.Status().Update(ctx, value); err != nil {
			return err
		}
		result.Messages, result.LastMessageID = messages, next
		return nil
	})
	return result, err
}

func addressedTo(message loopd.Message, actor loopd.ActorRef) bool {
	return (message.TargetKind == "" && message.TargetKey == "") ||
		(message.TargetKind == actor.Kind && message.TargetKey == actor.Key)
}
