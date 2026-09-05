package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"testing"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestChatCreatesOnlyAddressedInput(t *testing.T) {
	store := openServiceStore(t)
	ctx := context.Background()
	conversations := NewConversationService(store, nil)
	conversation, err := conversations.CreateConversation(ctx, "Planning", "alice")
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatService(store, nopChatRunner{}, nil, nil)
	input, err := chat.Create(ctx, conversation.ID, "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV7Pattern.MatchString(input.ID) || !uuidV7Pattern.MatchString(input.TaskID) ||
		input.Kind != loopd.RoleUser || input.Key != "alice" || input.TargetKind != loopd.RoleOperator || input.TargetKey != "router" {
		t.Fatalf("input = %+v", input)
	}
	history, err := store.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(history) != 1 || history[0].ID != input.ID || string(history[0].Content) != string(textContent("question")) {
		t.Fatalf("expected only the actual input: %+v %v", history, err)
	}
	history, err = store.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("input must not invent an answer: %+v %v", history, err)
	}
}

func TestChatAcceptsInputWithoutPageDelivery(t *testing.T) {
	store := openServiceStore(t)
	ctx := context.Background()
	conversation, err := NewConversationService(store, nil).CreateConversation(ctx, "Planning", "alice")
	if err != nil {
		t.Fatal(err)
	}
	chat := NewChatService(store, nil, nil, nil)
	_, err = chat.Create(ctx, conversation.ID, "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("question"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	history, err := store.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("committed input missing: %+v %v", history, err)
	}
}

func TestChatReportsDatabaseFailure(t *testing.T) {
	runner := &recordingChatRunner{}
	chat := NewChatService(failingCommitRepository{}, runner, nil, nil)
	_, err := chat.Create(context.Background(), "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("question"))
	if err == nil {
		t.Fatalf("commit error: %+v %v", runner, err)
	}
}

func TestConversationOwnershipAndTaskScope(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nopChatRunner{}, nil, nil)
	ctx := context.Background()

	root, err := conversations.CreateConversation(ctx, "Root", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, root.ID, "user-1", loopd.ActorRef{
		Kind: loopd.RoleOperator,
		Key:  "operator-1",
	}, textContent("question"))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := conversations.ActorConversation(ctx, root.ID, loopd.ActorRef{Kind: loopd.RoleOperator, Key: "operator-1"})
	if err != nil {
		t.Fatal(err)
	}
	if detail.ParentID != root.ID || detail.ActorKind != loopd.RoleOperator || detail.ActorKey != "operator-1" {
		t.Fatalf("work conversation ownership = %+v", detail)
	}
	if root.ActorKind != loopd.RoleUser || root.ActorKey != "user-1" || root.ParentID != "" {
		t.Fatalf("user conversation ownership = %+v", root)
	}
	if again, err := conversations.ActorConversation(ctx, root.ID, loopd.ActorRef{Kind: loopd.RoleOperator, Key: "operator-1"}); err != nil || again.ID != detail.ID {
		t.Fatalf("workspace not reused: %+v %v", again, err)
	}
	_, err = messages.CreateMessage(
		ctx, detail.ID, answer.TaskID, loopd.RoleOperator, "operator-1", textContent("working"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.CreateMessage(ctx, detail.ID, "other-task", loopd.RoleHarness, "call", textContent("another delivery")); err != nil {
		t.Fatalf("cross-task message error = %v", err)
	}
	if _, err := chat.Create(ctx, detail.ID, "user-1", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("nested chat")); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("nested chat error = %v", err)
	}
	// Switching the target of a later question must not transfer the user's
	// conversation to that target or reuse a previous Task's work conversation.
	other, err := chat.Create(ctx, root.ID, "user-1", loopd.ActorRef{Kind: loopd.RoleHarness, Key: "direct"}, textContent("next"))
	if err != nil {
		t.Fatal(err)
	}
	otherDetail, err := conversations.ActorConversation(ctx, root.ID, loopd.ActorRef{Kind: other.TargetKind, Key: other.TargetKey})
	if err != nil || otherDetail.ActorKind != loopd.RoleHarness || otherDetail.ActorKey != "direct" || otherDetail.ID == detail.ID {
		t.Fatalf("other work conversation = %+v, error = %v", otherDetail, err)
	}
	unchanged, err := conversations.GetConversation(ctx, root.ID)
	if err != nil || unchanged.ActorKind != root.ActorKind || unchanged.ActorKey != root.ActorKey || unchanged.ParentID != "" {
		t.Fatalf("user conversation changed = %+v, error = %v", unchanged, err)
	}
}

type nopChatRunner struct{}

type recordingChatRunner struct{}

func (*recordingChatRunner) Stream(
	context.Context,
	string,
	string,
	string,
	func(delivery.Event) error,
) error {
	return nil
}
func (nopChatRunner) Stream(
	context.Context,
	string,
	string,
	string,
	func(delivery.Event) error,
) error {
	return nil
}

type failingCommitRepository struct{}

func (failingCommitRepository) CreateChatInput(
	ctx context.Context,
	_ model.Message,
) (model.Message, error) {
	return model.Message{}, errors.New("commit failed")
}

func openServiceStore(t *testing.T) *repo.Store {
	t.Helper()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
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

func (nopChatRunner) EmitMessage(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}
func (runner *recordingChatRunner) EmitMessage(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}
