package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"testing"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestChatCreatesVisibleMessagesWithOneTask(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nil)
	ctx := context.Background()

	conversation, err := conversations.CreateConversation(ctx, "Planning", "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, conversation.ID, "user-1", loopd.ResponderRef{
		Kind: loopd.RoleHarness,
		Key:  "harness-1",
	}, textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV7Pattern.MatchString(conversation.ID) || !uuidV7Pattern.MatchString(answer.ID) ||
		!uuidV7Pattern.MatchString(answer.TaskID) {
		t.Fatalf("IDs are not UUIDv7: conversation=%q answer=%q task=%q", conversation.ID, answer.ID, answer.TaskID)
	}
	if answer.Kind != loopd.RoleHarness || answer.Key != "harness-1" {
		t.Fatalf("answer identity = %s/%s", answer.Kind, answer.Key)
	}

	history, err := messages.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	question := history[0]
	if question.Kind != loopd.RoleUser || question.Key != "user-1" {
		t.Fatalf("question identity = %s/%s", question.Kind, question.Key)
	}
	if question.TaskID != answer.TaskID || history[1].ID != answer.ID {
		t.Fatalf("messages do not share returned task: %#v", history)
	}

	var initial struct {
		Meta   map[string]string `json:"meta"`
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(answer.Content, &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Meta) != 0 || len(initial.Blocks) != 0 {
		t.Fatalf("initial answer content = %s", answer.Content)
	}

	updated, err := messages.UpdateMessageContent(ctx, conversation.ID, answer.ID, richContent())
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != answer.ID || updated.UpdatedAt.Before(answer.UpdatedAt) {
		t.Fatalf("updated answer = %#v", updated)
	}
}

func TestDetailConversationReferencesResponderMessage(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nil)
	ctx := context.Background()

	root, err := conversations.CreateConversation(ctx, "Root", "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, root.ID, "user-1", loopd.ResponderRef{
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
	if detail.ParentMessageID != answer.ID {
		t.Fatalf("parent_message_id = %q, want %q", detail.ParentMessageID, answer.ID)
	}
	if _, err := conversations.CreateConversation(ctx, "Duplicate", answer.ID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate detail error = %v, want %v", err, repo.ErrConflict)
	}
	detailMessage, err := messages.CreateMessage(
		ctx, detail.ID, answer.TaskID, loopd.RoleOperator, "operator-1", textContent("working"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.CreateConversation(ctx, "Nested", detailMessage.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested detail error = %v, want %v", err, ErrInvalid)
	}
}

func openServiceStore(t *testing.T) *repo.Store {
	t.Helper()
	store, err := repo.Open(repo.Config{Path: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func textContent(text string) json.RawMessage {
	value, _ := json.Marshal(map[string]any{
		"version": "1.0",
		"biz":     "chat",
		"meta":    map[string]any{},
		"blocks":  []map[string]any{{"id": "text", "type": "text", "content": text}},
	})
	return value
}

func richContent() json.RawMessage {
	return json.RawMessage(`{
		"version":"1.0",
		"biz":"chat",
		"meta":{},
		"blocks":[
			{"id":"answer","type":"text","content":"answer"},
			{"id":"tool-1","type":"tool","name":"search","status":"completed"}
		]
	}`)
}
