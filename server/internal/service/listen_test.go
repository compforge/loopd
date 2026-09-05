package service

import (
	"context"
	"errors"
	"testing"

	loopd "github.com/compforge/loopd"
	conversationv1 "github.com/compforge/loopd/runtime/api/v1alpha1"
	conversationclient "github.com/compforge/loopd/server/internal/conversation"
	"github.com/compforge/loopd/server/internal/model"
	kuberuntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestListenUsesTargetedSQLHistory(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	a := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "a"}
	b := loopd.ActorRef{Kind: loopd.RoleOperator, Key: "b"}
	coordinator := testConversationCoordinator(t)
	listen := NewListenService(store, coordinator, nil)
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
	// Deliberately lag the wake signals behind SQL. They must not limit Listen.
	if err := coordinator.Signal(ctx, "conv", "001", a); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Signal(ctx, "conv", "002", b); err != nil {
		t.Fatal(err)
	}
	result, err := listen.Listen(ctx, "conv", loopd.ListenRequest{Actor: a, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 || result.Messages[0].ID != "001" || result.Messages[1].ID != "004" {
		t.Fatalf("A's batch = %+v", result)
	}
	if result.Messages[0].TargetKind != loopd.RoleOperator || result.Messages[0].TargetKey != "a" {
		t.Fatal("target lost in public message mapping")
	}
	result, err = listen.Listen(ctx, "conv", loopd.ListenRequest{Actor: a, Limit: 2})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].ID != "006" {
		t.Fatalf("next batch = %+v, %v", result, err)
	}
	result, err = listen.Listen(ctx, "conv", loopd.ListenRequest{Actor: a})
	if err != nil || len(result.Messages) != 0 || result.LastMessageID != "006" {
		t.Fatalf("drained = %+v, %v", result, err)
	}
	other, err := listen.Listen(ctx, "conv", loopd.ListenRequest{Actor: b})
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

func (c *interruptedCoordinator) Signal(ctx context.Context, convID, messageID string, actor loopd.ActorRef) error {
	if c.fail {
		return errors.New("simulated Kubernetes interruption")
	}
	return c.ConversationCoordinator.Signal(ctx, convID, messageID, actor)
}

func TestListenRetriesCommittedNotification(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: "user", ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	coordinator := &interruptedCoordinator{ConversationCoordinator: testConversationCoordinator(t), fail: true}
	listen := NewListenService(store, coordinator, nil)
	chat := NewChatService(store, nopChatRunner{}, nil, listen)
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
	recovered := NewListenService(store, coordinator, nil)
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
	result, err := recovered.Listen(ctx, "conv", loopd.ListenRequest{Actor: target})
	if err != nil || len(result.Messages) != 1 || result.Messages[0].Kind != loopd.RoleUser {
		t.Fatalf("received = %+v, %v", result, err)
	}
	result, err = recovered.Listen(ctx, "conv", loopd.ListenRequest{Actor: target})
	if err != nil || len(result.Messages) != 0 {
		t.Fatalf("duplicate delivery = %+v, %v", result, err)
	}
}
