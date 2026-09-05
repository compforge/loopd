package service

import (
	"context"
	"errors"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
	"testing"
)

func TestMessageContextHasBoundedHistoryWithoutAnswerPair(t *testing.T) {
	store := openServiceStore(t)
	ctx := context.Background()
	convs := NewConversationService(store, nil)
	conv, err := convs.CreateConversation(ctx, "Shared", "alice")
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatService(store, nopChatRunner{}, nil, nil)
	input, err := chat.Create(ctx, conv.ID, "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("hello"))
	if err != nil {
		t.Fatal(err)
	}
	messages := NewMessageService(store, nil)
	speech, err := messages.Speak(ctx, conv.ID, loopd.SpeakRequest{Key: "progress", Actor: loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, Content: textContent("Working")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.Speak(ctx, conv.ID, loopd.SpeakRequest{Key: "more", Actor: loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, Content: textContent("More")}); err != nil {
		t.Fatal(err)
	}
	service := NewContextService(store, nil)
	result, err := service.GetMessageContext(ctx, conv.ID, speech.ID)
	if err != nil || result.Message.ID != speech.ID || len(result.History) != 2 || result.History[0].ID != input.ID || result.HasEarlier {
		t.Fatalf("context=%+v %v", result, err)
	}
	if _, err := service.GetMessageContext(ctx, "other", speech.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross conv=%v", err)
	}
	workspace, err := convs.ActorConversation(ctx, conv.ID, loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"})
	if err != nil {
		t.Fatal(err)
	}
	same, err := convs.ActorConversation(ctx, conv.ID, loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"})
	if err != nil || same.ID != workspace.ID {
		t.Fatalf("workspace=%+v %v", same, err)
	}
}
