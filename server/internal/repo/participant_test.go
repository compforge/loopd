package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
)

// +case=`定向输入与过程会话原子提交；完整 Actor 身份隔离且并发重复输入复用同一会话。`
func TestParticipantAllocationIsAtomicAndActorScoped(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	content := []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)
	actor := contract.ActorRef{Kind: "operator/custom/manager", Key: "same"}
	if _, err := s.ParticipantConversation(ctx, "conv", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notification lookup allocated missing association: %v", err)
	}
	input := model.Message{ID: "input", ConversationID: "conv", SourceKind: contract.ActorKindUser, TargetKind: actor.Kind, TargetKey: actor.Key, Content: content}
	if _, err := s.CreateChatInput(ctx, input); err == nil {
		t.Fatal("duplicate message must fail")
	}
	if _, err := s.FindActorConversation(ctx, "conv", actor.Kind, actor.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed input leaked detail: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			message := input
			message.ID = fmt.Sprintf("input-%d", i)
			if _, err := s.CreateChatInput(ctx, message); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	detail, err := s.FindActorConversation(ctx, "conv", actor.Kind, actor.Key)
	if err != nil || detail.ParentID == nil || *detail.ParentID != "conv" {
		t.Fatalf("detail=%+v %v", detail, err)
	}
	var count int64
	if err := s.db.Model(&model.Conversation{}).Where("parent_id = ?", "conv").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate details: %d %v", count, err)
	}
	for i, target := range []contract.ActorRef{{Kind: "harness", Key: "same"}, {Kind: actor.Kind, Key: "other"}} {
		req := contract.SpeakRequest{Status: contract.MessageStatusStreaming, Key: fmt.Sprint(i), Actor: actor, Target: target, Content: content}
		msg, err := s.Speak(ctx, "conv", req)
		if err != nil {
			t.Fatal(err)
		}
		child, err := s.FindActorConversation(ctx, "conv", target.Kind, target.Key)
		if err != nil || child.ID == detail.ID {
			t.Fatalf("target reused another actor: %+v %v", child, err)
		}
		repeated, err := s.Speak(ctx, "conv", req)
		if err != nil || repeated.ID != msg.ID {
			t.Fatalf("speak retry: %+v %v", repeated, err)
		}
	}
	if _, err := s.CreateConversation(ctx, model.Conversation{ID: "another", ActorKind: contract.ActorKindUser, ActorKey: "alice"}); err != nil {
		t.Fatal(err)
	}
	input.ID, input.ConversationID = "another-input", "another"
	if _, err := s.CreateChatInput(ctx, input); err != nil {
		t.Fatal(err)
	}
	other, err := s.FindActorConversation(ctx, "another", actor.Kind, actor.Key)
	if err != nil || other.ID == detail.ID {
		t.Fatalf("parent isolation: %+v %v", other, err)
	}
}

// +case=`广播、面向 User 的发言与过程会话内的定向协作不会递归分配过程会话。`
func TestParticipantAllocationOnlyForDirectedRootMessages(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	content := []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)
	actor := contract.ActorRef{Kind: "operator", Key: "router"}
	for i, target := range []contract.ActorRef{{}, {Kind: "user", Key: "alice"}} {
		if _, err := s.Speak(ctx, "conv", contract.SpeakRequest{Key: fmt.Sprint(i), Actor: actor, Target: target, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := s.db.Model(&model.Conversation{}).Where("parent_id IS NOT NULL").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected allocation: %d %v", count, err)
	}
	parent := "conv"
	if _, err := s.CreateConversation(ctx, model.Conversation{ID: "detail", ParentID: &parent, ActorKind: actor.Kind, ActorKey: actor.Key}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Speak(ctx, "detail", contract.SpeakRequest{Key: "role", Actor: actor, Target: contract.ActorRef{Kind: "operator/longhorizon/manager", Key: "run"}, Content: content}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&model.Conversation{}).Where("parent_id = ?", "detail").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("recursive allocation: %d %v", count, err)
	}
}

func TestHumanReplyAllocatesRecipientDetail(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	r := question("allocation")
	q, err := s.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplyHuman(ctx, "conv", "alice", contract.HumanReply{ReplyToID: q.Message.ID, Outcome: contract.HumanSuccess, Value: "small"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindActorConversation(ctx, "conv", r.Actor.Kind, r.Actor.Key); err != nil {
		t.Fatal(err)
	}
}
