package service

import (
	"context"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
)

// +case=`无浏览器时推进 timeout；维护循环可重入`
func TestHumanMaintenanceRecoversNotifications(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingChatRunner{}
	chat := NewChatService(store, runner, nil, nil)
	message, err := chat.Create(ctx, "conv", "alice", loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	human := NewHumanService(store, nil)
	q, err := human.Create(ctx, loopd.HumanRequest{ConversationID: message.ConversationID, Actor: loopd.ActorRef{Kind: message.TargetKind, Key: message.TargetKey}, Target: loopd.ActorRef{Kind: message.Kind, Key: message.Key}, ReplyToID: message.ID, EffectKey: "ask", Type: "ask", Title: "Question", Prompt: "Reply", Timeout: time.Nanosecond, AllowOther: true})
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
	if err := recovered.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
}
