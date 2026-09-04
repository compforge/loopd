package service

import (
	"context"
	"testing"

	loopd "github.com/compforge/loopd"
)

func TestTaskContextComesFromMessages(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	chat := NewChatService(store, nopTaskMarker{}, nil)
	tasks := NewTaskService(store, nil)
	ctx := context.Background()

	conversation, err := conversations.CreateConversation(ctx, "Planning", "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, conversation.ID, "user-1", loopd.ResponderRef{
		Kind: loopd.RoleOperator,
		Key:  "operator-1",
	}, textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := conversations.CreateConversation(ctx, "Operator work", answer.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages := NewMessageService(store, nil)
	if _, err := messages.CreateMessage(
		ctx, detail.ID, answer.TaskID, loopd.RoleHarness, "harness-1", textContent("internal result"),
	); err != nil {
		t.Fatal(err)
	}
	taskContext, err := tasks.GetContext(ctx, answer.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if taskContext.ID != answer.TaskID || taskContext.Conversation.ID != conversation.ID {
		t.Fatalf("Task context identity = %#v", taskContext)
	}
	if taskContext.Input.Kind != loopd.RoleUser || taskContext.Response.ID != answer.ID {
		t.Fatalf("Task messages = input %#v response %#v", taskContext.Input, taskContext.Response)
	}
	if len(taskContext.History) != 1 || taskContext.History[0].ID != taskContext.Input.ID || taskContext.HasEarlier {
		t.Fatalf("Task history = %#v, has_earlier=%t", taskContext.History, taskContext.HasEarlier)
	}
}
