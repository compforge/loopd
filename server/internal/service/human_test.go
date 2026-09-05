package service

import (
	"context"
	"errors"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
)

type humanTasks struct {
	recordingTaskClient
	wakeErr error
	wakes   int
}

func (t *humanTasks) Wake(context.Context, string) error         { t.wakes++; return t.wakeErr }
func (*humanTasks) Exists(context.Context, string) (bool, error) { return true, nil }

// +case=`无浏览器时推进 timeout；唤醒失败和 Task 删除失败在新 Service 中恢复`
func TestHumanMaintenanceRecoversNotificationsAndCompletion(t *testing.T) {
	ctx := context.Background()
	store := openServiceStore(t)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	tasks := &humanTasks{wakeErr: errors.New("offline")}
	runner := &recordingChatRunner{}
	chat := NewChatService(store, tasks, runner, nil)
	message, err := chat.Create(ctx, "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	human := NewHumanService(store, tasks, nil)
	q, err := human.Create(ctx, loopd.HumanRequest{TaskID: message.TaskID, EffectKey: "ask", Type: "ask", Title: "Question", Prompt: "Reply", Timeout: time.Nanosecond, AllowOther: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := human.Maintain(ctx, chat); err == nil {
		t.Fatal("expected failed wake delivery")
	}
	result, err := human.Get(ctx, q.Message.ID)
	if err != nil || result.Status != loopd.HumanTimeout || result.Reply != nil {
		t.Fatalf("timeout=%+v %v", result, err)
	}
	pending, err := store.HumanMaintenance(ctx)
	if err != nil || len(pending) != 1 || !pending[0].WakePending {
		t.Fatalf("lost pending wake=%+v %v", pending, err)
	}
	recoveredTasks := &humanTasks{}
	recovered := NewHumanService(store, recoveredTasks, nil)
	recoveredChat := NewChatService(store, recoveredTasks, runner, nil)
	if err := recovered.Maintain(ctx, recoveredChat); err != nil {
		t.Fatal(err)
	}
	if recoveredTasks.wakes != 1 {
		t.Fatal("wake not retried")
	}
	recoveredTasks.deleteErr = errors.New("offline")
	if err := recoveredChat.Complete(ctx, message.TaskID, nil); err == nil {
		t.Fatal("delete failure not surfaced")
	}
	pending, err = store.HumanMaintenance(ctx)
	if err != nil || len(pending) != 1 || pending[0].DeliveryState != "closing" {
		t.Fatalf("completion intent=%+v %v", pending, err)
	}
	recoveredTasks.deleteErr = nil
	if err := recovered.Maintain(ctx, recoveredChat); err != nil {
		t.Fatal(err)
	}
	response, err := store.GetMessage(ctx, message.ID)
	if err != nil || response.DeliveryState != "closed" {
		t.Fatalf("not closed=%+v %v", response, err)
	}
}
