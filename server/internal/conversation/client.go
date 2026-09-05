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

// NewClient requires a direct (uncached) Kubernetes client. Poll's conflict
// retries must observe the latest cursor, not a controller cache's old version.
func NewClient(kube client.Client, namespace string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{kube: kube, namespace: namespace, timeout: timeout}
}

// Signal records a committed message's recipient. An empty target is an
// explicit broadcast to existing participants; it never registers all Operators.
func (c *Client) Signal(ctx context.Context, conversationID, messageID string, target loopd.ActorRef, revision uint64) error {
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
		if value.Annotations == nil {
			value.Annotations = map[string]string{}
		}
		wake := func(kind, key string) {
			annotation := conversationv1.WakeAnnotation(kind, key)
			stamp := fmt.Sprintf("%s/%d", messageID, revision)
			if value.Annotations[annotation] != stamp {
				value.Annotations[annotation] = stamp
				changed = true
			}
		}
		for i := range value.Spec.Participants {
			participant := &value.Spec.Participants[i]
			if target == (loopd.ActorRef{}) || (participant.Kind == string(target.Kind) && participant.Key == target.Key) {
				found = true
				wake(participant.Kind, participant.Key)
				if participant.EndOffset < messageID {
					participant.EndOffset, changed = messageID, true
				}
			}
		}
		if !found && target != (loopd.ActorRef{}) {
			wake(string(target.Kind), target.Key)
			value.Spec.Participants = append(value.Spec.Participants, conversationv1.ConversationParticipant{
				Kind: string(target.Kind), Key: target.Key, EndOffset: messageID,
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

// Poll records receipt, not commitment. Repeating a request after a lost response
// reads the same uncommitted range; only Commit changes the recovery position.
// +spec=`Poll 不自动提交；失败或重启可从已提交位置重新接收`
func (c *Client) Poll(ctx context.Context, conversationID string, actor loopd.ActorRef, afterID string, read ReadMessages) (loopd.PollResult, error) {
	if conversationID == "" || !actor.ValidTarget() || read == nil {
		return loopd.PollResult{}, errors.New("conversation, actor and message reader are required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var result loopd.PollResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		result = loopd.PollResult{Messages: []loopd.Message{}}
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
		after := value.Committed(string(actor.Kind), actor.Key)
		result.Committed = after
		result.EndOffset = value.EndOffset(string(actor.Kind), actor.Key)
		if afterID > after {
			after = afterID
		}
		result.Position = after
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
				return fmt.Errorf("invalid Poll batch for conversation %q", conversationID)
			}
			next = message.ID
		}
		found := false
		for i := range value.Status.Consumers {
			consumer := &value.Status.Consumers[i]
			if consumer.Kind == string(actor.Kind) && consumer.Key == actor.Key {
				if consumer.Position < next {
					consumer.Position = next
				}
				found = true
				break
			}
		}
		if !found {
			value.Status.Consumers = append(value.Status.Consumers, conversationv1.ConversationConsumer{
				Kind: string(actor.Kind), Key: actor.Key, Position: next,
			})
		}
		if err := c.kube.Status().Update(ctx, value); err != nil {
			return err
		}
		result.Messages, result.Position = messages, next
		return nil
	})
	return result, err
}

func addressedTo(message loopd.Message, actor loopd.ActorRef) bool {
	return (message.TargetKind == "" && message.TargetKey == "") ||
		(message.TargetKind == actor.Kind && message.TargetKey == actor.Key)
}

var ErrInvalidCommit = errors.New("commit exceeds received position or has no message ID")

// Commit monotonically stores the safe recovery boundary. Callers must only
// acknowledge a contiguous processed prefix, never a later parallel result
// while an earlier input has not been handled or durably adopted.
func (c *Client) Commit(ctx context.Context, conversationID string, request loopd.CommitRequest) error {
	if !request.Actor.ValidTarget() || request.Through == "" {
		return ErrInvalidCommit
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		value := &conversationv1.Conversation{}
		if err := c.kube.Get(ctx, client.ObjectKey{Name: conversationID, Namespace: c.namespace}, value); err != nil {
			return err
		}
		for i := range value.Status.Consumers {
			consumer := &value.Status.Consumers[i]
			if consumer.Kind != string(request.Actor.Kind) || consumer.Key != request.Actor.Key {
				continue
			}
			if request.Through <= consumer.Committed {
				return nil
			}
			if request.Through > consumer.Position {
				return ErrInvalidCommit
			}
			consumer.Committed = request.Through
			return c.kube.Status().Update(ctx, value)
		}
		return ErrNotParticipant
	})
}
