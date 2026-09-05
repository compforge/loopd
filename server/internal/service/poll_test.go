package service

import (
	"context"
	"errors"
	"testing"

	ui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	loopruntime "github.com/compforge/loopd/runtime"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	conversationclient "github.com/compforge/loopd/server/internal/conversation"
	"github.com/compforge/loopd/server/internal/model"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func testConversationCoordinator(t *testing.T) ConversationCoordinator {
	t.Helper()
	scheme := kuberuntime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	return conversationclient.NewClient(kube, "test", 0)
}

// +case=`Actors exchange durable messages without a UI task; Poll waits for an open earlier message, End wakes it, and Commit stays actor-local.`
func TestActorsConsumeCompletedSpeechIndependently(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	scheme := kuberuntime.NewScheme()
	if err := conversationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&conversationv1.Conversation{}).Build()
	poll := NewPollService(store, conversationclient.NewClient(kube, "test", 0), nil)
	a := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"}
	b := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "b"}
	c := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "c"}
	say := func(key string, target loopd.ActorRef, stream bool) model.Message {
		t.Helper()
		message, err := store.Speak(ctx, "conv", loopd.SpeakRequest{Key: key, Actor: a, Target: target, Stream: stream, Content: textContent(key)})
		if err != nil {
			t.Fatal(err)
		}
		if message.TaskID != "" {
			t.Fatal("speech acquired a page task")
		}
		return message
	}
	first := say("stream", b, true)
	later := say("ready", b, false)
	if first.ID >= later.ID {
		t.Fatal("fixture requires ordered IDs")
	}
	third := say("independent", c, false)
	if err := poll.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, err := poll.Poll(ctx, "conv", loopd.PollRequest{Actor: b})
	if err != nil || len(inbox.Messages) != 0 || inbox.Position != "" {
		t.Fatalf("consumed incomplete prefix: %+v %v", inbox, err)
	}
	other, err := poll.Poll(ctx, "conv", loopd.PollRequest{Actor: c})
	if err != nil || len(other.Messages) != 1 || other.Messages[0].ID != third.ID {
		t.Fatalf("unrelated actor blocked: %+v %v", other, err)
	}
	old := &conversationv1.Conversation{}
	key := client.ObjectKey{Namespace: "test", Name: "conv"}
	if err := kube.Get(ctx, key, old); err != nil {
		t.Fatal(err)
	}
	if err := store.ProjectOutput(ctx, first.ID, ui.End(2)); err != nil {
		t.Fatal(err)
	}
	if err := poll.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	next := &conversationv1.Conversation{}
	if err := kube.Get(ctx, key, next); err != nil {
		t.Fatal(err)
	}
	if next.EndOffset("operator", "b") != later.ID || old.EndOffset("operator", "b") != later.ID {
		t.Fatal("EndOffset should remain at the later message")
	}
	change := event.UpdateEvent{ObjectOld: old, ObjectNew: next}
	if !loopruntime.ConversationPredicate(b).Update(change) || loopruntime.ConversationPredicate(c).Update(change) {
		t.Fatal("ending an earlier stream must wake only its recipient")
	}
	inbox, err = poll.Poll(ctx, "conv", loopd.PollRequest{Actor: b})
	if err != nil || len(inbox.Messages) != 2 || !inbox.Messages[0].Ended() || inbox.Position != later.ID {
		t.Fatalf("complete prefix: %+v %v", inbox, err)
	}
	if err := poll.Commit(ctx, "conv", loopd.CommitRequest{Actor: b, Through: inbox.Position}); err != nil {
		t.Fatal(err)
	}
	other, err = poll.Poll(ctx, "conv", loopd.PollRequest{Actor: c})
	if err != nil || len(other.Messages) != 1 {
		t.Fatalf("B committed C's input: %+v %v", other, err)
	}
}

func TestPollUsesTargetedSQLHistory(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	a := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"}
	b := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "b"}
	coordinator := testConversationCoordinator(t)
	poll := NewPollService(store, coordinator, nil)
	for _, message := range []model.Message{
		{ID: "001", Kind: "user", ActorKey: "alice", TargetKind: "operator", TargetKey: "a"},
		{ID: "002", Kind: "user", ActorKey: "alice", TargetKind: "operator", TargetKey: "b"},
		{ID: "003", Kind: "operator", ActorKey: "b", TargetKind: "user", TargetKey: "alice"},
		{ID: "004", Kind: "user", ActorKey: "alice"},
		{ID: "005", Kind: "operator", ActorKey: "a"},
		{ID: "006", Kind: "user", ActorKey: "alice", TargetKind: "operator", TargetKey: "a"},
	} {
		message.ConversationID, message.TaskID, message.Content = "conv", "ui-chat", textContent(message.ID)
		if _, err := store.CreateMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately lag the wake signals behind SQL. They must not limit Poll.
	if err := coordinator.Signal(ctx, "conv", "001", a, 1); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Signal(ctx, "conv", "002", b, 1); err != nil {
		t.Fatal(err)
	}
	result, err := poll.Poll(ctx, "conv", loopd.PollRequest{Actor: a, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 || result.Messages[0].ID != "001" || result.Messages[1].ID != "004" {
		t.Fatalf("A's batch = %+v", result)
	}
	if result.Messages[0].TargetKind != loopd.RoleOperator || result.Messages[0].TargetKey != "a" {
		t.Fatal("target lost in public message mapping")
	}
	repeated, err := poll.Poll(ctx, "conv", loopd.PollRequest{Actor: a, Limit: 2})
	if err != nil || repeated.Messages[0].ID != result.Messages[0].ID {
		t.Fatalf("uncommitted replay: %+v %v", repeated, err)
	}
	result, err = poll.Poll(ctx, "conv", loopd.PollRequest{Actor: a, Limit: 2, After: result.Position})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].ID != "006" {
		t.Fatalf("next batch = %+v, %v", result, err)
	}
	if err := poll.Commit(ctx, "conv", loopd.CommitRequest{Actor: a, Through: result.Position}); err != nil {
		t.Fatal(err)
	}
	result, err = poll.Poll(ctx, "conv", loopd.PollRequest{Actor: a})
	if err != nil || len(result.Messages) != 0 || result.Position != "006" {
		t.Fatalf("drained = %+v, %v", result, err)
	}
	other, err := poll.Poll(ctx, "conv", loopd.PollRequest{Actor: b})
	if err != nil || len(other.Messages) != 3 || other.Messages[0].ID != "002" {
		t.Fatalf("independent B = %+v, %v", other, err)
	}
	// Reading all messages remains independent of recipient filtering and cursors.
	history, err := NewMessageService(store, nil).ListMessages(ctx, "conv", "", 100)
	if err != nil || len(history) != 6 {
		t.Fatalf("history = %+v, %v", history, err)
	}
}

type interruptedCoordinator struct {
	ConversationCoordinator
	fail bool
}

func (c *interruptedCoordinator) Signal(ctx context.Context, convID, messageID string, actor loopd.ActorRef, revision uint64) error {
	if c.fail {
		return errors.New("simulated Kubernetes interruption")
	}
	return c.ConversationCoordinator.Signal(ctx, convID, messageID, actor, revision)
}

func TestPollRetriesCommittedNotification(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	coordinator := &interruptedCoordinator{ConversationCoordinator: testConversationCoordinator(t), fail: true}
	poll := NewPollService(store, coordinator, nil)
	chat := NewChatService(store, nopChatRunner{}, nil, poll)
	target := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}
	if _, err := chat.Create(ctx, "conv", "alice", target, textContent("hello")); err != nil {
		t.Fatalf("notification failure must not ask the user to resend committed input: %v", err)
	}
	pending, err := store.PendingDispatches(ctx, 100)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if pending[0].Kind != "user" || pending[0].TargetKey != "router" {
		t.Fatalf("pending message = %+v", pending[0])
	}
	coordinator.fail = false
	// A different service instance can finish the durable notification.
	recovered := NewPollService(store, coordinator, nil)
	if err := recovered.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingDispatches(ctx, 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after retry = %+v, %v", pending, err)
	}
	result, err := recovered.Poll(ctx, "conv", loopd.PollRequest{Actor: target})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Kind != loopd.RoleUser {
		t.Fatalf("received = %+v, %v", result, err)
	}
	if err := recovered.Commit(ctx, "conv", loopd.CommitRequest{Actor: target, Through: result.Position}); err != nil {
		t.Fatal(err)
	}
	result, err = recovered.Poll(ctx, "conv", loopd.PollRequest{Actor: target})
	if err != nil || len(result.Messages) != 0 {
		t.Fatalf("duplicate delivery = %+v, %v", result, err)
	}
}
