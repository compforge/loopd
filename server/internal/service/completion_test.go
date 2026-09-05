package service

import (
	"context"
	"errors"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"testing"
)

// +case=`没有 Human 请求时，流完成失败仍由 Chat 生命周期恢复`
func TestCompletionMaintenanceWithoutHuman(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingChatRunner{}
	chat := NewChatService(store, runner, nil, nil)
	response, err := chat.Create(ctx, "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	runner.completeErr = errors.New("offline")
	if err := chat.Complete(ctx, response.TaskID, nil); err == nil {
		t.Fatal("expected delivery failure")
	}
	if rows, err := store.HumanMaintenance(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("Human owns task completion: %v %v", rows, err)
	}
	runner.completeErr = nil
	recovered := NewChatService(store, runner, nil, nil)
	if err := recovered.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetMessage(ctx, response.ID)
	if err != nil || row.DeliveryState != "closed" {
		t.Fatalf("completion=%+v %v", row, err)
	}
}
