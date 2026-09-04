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

func TestConversationMessagesUseUUIDv7AndParticipantIdentity(t *testing.T) {
	store, err := repo.Open(repo.Config{Path: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := New(store, nil)
	ctx := context.Background()

	conversation, err := service.CreateConversation(ctx, "Planning", "")
	if err != nil {
		t.Fatal(err)
	}
	question, err := service.CreateMessage(ctx, conversation.ID, loopd.RoleUser, "user-1", textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	answer, err := service.CreateMessage(ctx, conversation.ID, loopd.RoleHarness, "harness-1", richContent())
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{conversation.ID, question.ID, answer.ID} {
		if !uuidV7Pattern.MatchString(id) {
			t.Fatalf("id %q is not UUIDv7", id)
		}
	}
	if !(conversation.ID < question.ID && question.ID < answer.ID) {
		t.Fatalf("UUIDv7 order = %s, %s, %s", conversation.ID, question.ID, answer.ID)
	}
	if question.Kind != loopd.RoleUser || question.Key != "user-1" ||
		answer.Kind != loopd.RoleHarness || answer.Key != "harness-1" {
		t.Fatalf("messages = %#v / %#v", question, answer)
	}
	var content struct {
		Blocks []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(answer.Content, &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Blocks) != 2 || content.Blocks[1].Type != "tool" {
		t.Fatalf("answer content = %s", answer.Content)
	}
}

func TestDetailConversationReferencesRootMessage(t *testing.T) {
	store, err := repo.Open(repo.Config{Path: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := New(store, nil)
	ctx := context.Background()

	root, err := service.CreateConversation(ctx, "Root", "")
	if err != nil {
		t.Fatal(err)
	}
	question, err := service.CreateMessage(ctx, root.ID, loopd.RoleUser, "user-1", textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.CreateConversation(ctx, "Operator work", question.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ParentMessageID != question.ID {
		t.Fatalf("parent_message_id = %q, want %q", detail.ParentMessageID, question.ID)
	}

	if _, err := service.CreateConversation(ctx, "Duplicate", question.ID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate detail error = %v, want %v", err, repo.ErrConflict)
	}
	detailMessage, err := service.CreateMessage(ctx, detail.ID, loopd.RoleOperator, "operator-1", textContent("working"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateConversation(ctx, "Nested", detailMessage.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested detail error = %v, want %v", err, ErrInvalid)
	}
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
