package service

import (
	"context"
	"errors"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
)

// +case=`无浏览器时推进 timeout；Chat 完成失败由自己的维护循环恢复`
func TestHumanMaintenanceRecoversNotificationsAndCompletion(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingChatRunner{}
	chat := NewChatService(store, runner, nil, nil)
	message, err := chat.Create(ctx, "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	human := NewHumanService(store, nil)
	q, err := human.Create(ctx, loopd.HumanRequest{ConversationID: message.ConversationID, Actor: loopd.ActorRef{Kind: message.TargetKind, Key: message.TargetKey}, Target: loopd.ActorRef{Kind: message.Kind, Key: message.Key}, ReplyToID: message.ID, TaskID: message.TaskID, EffectKey: "ask", Type: "ask", Title: "Question", Prompt: "Reply", Timeout: time.Nanosecond, AllowOther: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := human.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := human.Get(ctx, q.Message.ID)
	if err != nil || result.Status != loopd.HumanTimeout || result.Reply != nil {
		t.Fatalf("timeout=%+v %v", result, err)
	}
	pending, err := store.HumanMaintenance(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("lost pending wake=%+v %v", pending, err)
	}
	recovered := NewHumanService(store, nil)
	recoveredChat := NewChatService(store, runner, nil, nil)
	if err := recovered.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	runner.completeErr = errors.New("offline")
	if err := recoveredChat.Complete(ctx, message.TaskID, nil); err == nil {
		t.Fatal("delivery failure not surfaced")
	}
	pending, err = store.PendingCompletions(ctx)
	if err != nil || len(pending) != 1 || pending[0].DeliveryState != "closing" {
		t.Fatalf("completion intent=%+v %v", pending, err)
	}
	runner.completeErr = nil
	if err := recoveredChat.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	response, err := store.GetMessage(ctx, message.ID)
	if err != nil || response.DeliveryState != "closed" {
		t.Fatalf("not closed=%+v %v", response, err)
	}
}
