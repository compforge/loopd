package service

import (
	"context"
	"testing"

	loopd "github.com/compforge/loopd"
)

func TestChatContextDoesNotRequireResponse(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	chat := NewChatService(store, nopChatRunner{}, nil, nil)
	tasks := NewChatContextService(store, nil)
	ctx := context.Background()

	conversation, err := conversations.CreateConversation(ctx, "Planning", "user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, conversation.ID, "user-1", loopd.ActorRef{
		Kind: loopd.RoleOperator,
		Key:  "operator-1",
	}, textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := conversations.CreateConversation(ctx, "Operator work", "", answer.TaskID)
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
	if taskContext.Input.Kind != loopd.RoleUser || taskContext.Response.ID != "" || taskContext.Input.ID != answer.ID {
		t.Fatalf("Task messages = input %#v response %#v", taskContext.Input, taskContext.Response)
	}
	if len(taskContext.History) != 1 || taskContext.History[0].ID != taskContext.Input.ID || taskContext.HasEarlier {
		t.Fatalf("Task history = %#v, has_earlier=%t", taskContext.History, taskContext.HasEarlier)
	}
	// Later user input is part of the same task's visible context, but it must
	// not replace the original input or change its history cutoff.
	if _, err := messages.CreateMessage(ctx, conversation.ID, answer.TaskID, loopd.RoleUser, "user-1", textContent("follow-up")); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Create(ctx, conversation.ID, "user-1", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "another"}, textContent("other task")); err != nil {
		t.Fatal(err)
	}
	all, err := tasks.ListMessages(ctx, answer.TaskID, "", 100)
	if err != nil || len(all) != 3 {
		t.Fatalf("task messages = %+v, error = %v", all, err)
	}
	if all[1].ConversationID != detail.ID || all[2].Kind != loopd.RoleUser {
		t.Fatalf("task scopes = %+v", all)
	}
	page, err := tasks.ListMessages(ctx, answer.TaskID, all[1].ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != all[2].ID {
		t.Fatalf("page = %+v, error = %v", page, err)
	}
	main, err := messages.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(main) != 3 {
		t.Fatalf("main history = %+v, error = %v", main, err)
	}
	again, err := tasks.GetContext(ctx, answer.TaskID)
	if err != nil || again.Input.ID != taskContext.Input.ID || len(again.History) != 1 {
		t.Fatalf("original context changed = %+v, error = %v", again, err)
	}
}
