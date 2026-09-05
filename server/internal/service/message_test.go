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
	conversation, err := conversations.CreateConversation(ctx, "Planning", "alice", "")
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
	if err := chat.Complete(ctx, input.TaskID, nil); err != nil {
		t.Fatal(err)
	}
	history, err = store.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(history) != 1 || history[0].DeliveryState != "closed" {
		t.Fatalf("completion must not invent an answer: %+v %v", history, err)
	}
}

func TestChatRollsBackInputWhenStreamInitializationFails(t *testing.T) {
	store := openServiceStore(t)
	ctx := context.Background()
	conversation, err := NewConversationService(store, nil).CreateConversation(ctx, "Planning", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingChatRunner{initializeErr: errors.New("offline")}
	chat := NewChatService(store, runner, nil, nil)
	_, err = chat.Create(ctx, conversation.ID, "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("question"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Create = %v", err)
	}
	history, err := store.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil || len(history) != 0 {
		t.Fatalf("uncommitted input survived: %+v %v", history, err)
	}
}

func TestChatDeletesStreamWhenCommitFails(t *testing.T) {
	runner := &recordingChatRunner{}
	chat := NewChatService(failingCommitRepository{}, runner, nil, nil)
	_, err := chat.Create(context.Background(), "conv", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, textContent("question"))
	if err == nil || runner.initializedTaskID == "" || runner.deletedTaskID != runner.initializedTaskID {
		t.Fatalf("commit compensation: %+v %v", runner, err)
	}
}

func TestConversationOwnershipAndTaskScope(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nopChatRunner{}, nil, nil)
	ctx := context.Background()

	root, err := conversations.CreateConversation(ctx, "Root", "user-1", "")
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
	detail, err := conversations.CreateConversation(ctx, "Operator work", "", answer.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TaskID != answer.TaskID || detail.ActorKind != loopd.RoleOperator || detail.ActorKey != "operator-1" {
		t.Fatalf("work conversation ownership = %+v", detail)
	}
	if root.ActorKind != loopd.RoleUser || root.ActorKey != "user-1" || root.TaskID != "" {
		t.Fatalf("user conversation ownership = %+v", root)
	}
	if _, err := conversations.CreateConversation(ctx, "Duplicate", "", answer.TaskID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate detail error = %v, want %v", err, repo.ErrConflict)
	}
	_, err = messages.CreateMessage(
		ctx, detail.ID, answer.TaskID, loopd.RoleOperator, "operator-1", textContent("working"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.CreateMessage(ctx, detail.ID, "other-task", loopd.RoleHarness, "call", textContent("wrong task")); !errors.Is(err, repo.ErrConflict) {
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
	otherDetail, err := conversations.CreateConversation(ctx, "Harness work", "", other.TaskID)
	if err != nil || otherDetail.ActorKind != loopd.RoleHarness || otherDetail.ActorKey != "direct" || otherDetail.ID == detail.ID {
		t.Fatalf("other work conversation = %+v, error = %v", otherDetail, err)
	}
	unchanged, err := conversations.GetConversation(ctx, root.ID)
	if err != nil || unchanged.ActorKind != root.ActorKind || unchanged.ActorKey != root.ActorKey || unchanged.TaskID != "" {
		t.Fatalf("user conversation changed = %+v, error = %v", unchanged, err)
	}
}

type nopChatRunner struct{}

func (nopChatRunner) Initialize(context.Context, string, json.RawMessage) error { return nil }
func (nopChatRunner) Delete(context.Context, string) error                      { return nil }
func (nopChatRunner) Emit(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}

type recordingChatRunner struct {
	initializedTaskID string
	deletedTaskID     string
	completedTaskID   string
	completeErr       error
	initializeErr     error
}

func (runner *recordingChatRunner) Initialize(_ context.Context, taskID string, _ json.RawMessage) error {
	runner.initializedTaskID = taskID
	return runner.initializeErr
}

func (runner *recordingChatRunner) Delete(_ context.Context, taskID string) error {
	runner.deletedTaskID = taskID
	return nil
}

func (*recordingChatRunner) Emit(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}

func (runner *recordingChatRunner) Complete(_ context.Context, taskID string, _ *delivery.Failure) error {
	runner.completedTaskID = taskID
	return runner.completeErr
}

func (*recordingChatRunner) Stream(
	context.Context,
	string,
	string,
	string,
	func(delivery.Event) error,
) error {
	return nil
}
func (nopChatRunner) Complete(context.Context, string, *delivery.Failure) error { return nil }
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
	beforeCommit func(context.Context) error,
) (model.Message, error) {
	if err := beforeCommit(ctx); err != nil {
		return model.Message{}, err
	}
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

func (failingCommitRepository) BeginCompletion(context.Context, string, []byte, bool) error {
	return nil
}
func (failingCommitRepository) FinishCompletion(context.Context, string) error { return nil }

func completionStore(t *testing.T) *repo.Store {
	t.Helper()
	store := openServiceStore(t)
	ctx := context.Background()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conversation-1"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateChatMessages(ctx,
		model.Message{ID: "input", ConversationID: "conversation-1", TaskID: "task-1", Kind: "user", ActorKey: "alice", Content: textContent("question")},
		model.Message{ID: "response", ConversationID: "conversation-1", TaskID: "task-1", Kind: "operator", ActorKey: "router", Content: textContent("")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func (failingCommitRepository) PendingCompletions(context.Context) ([]model.Message, error) {
	return nil, nil
}

func (nopChatRunner) Output(context.Context, string, loopd.OutputRequest) (loopd.Message, error) {
	return loopd.Message{}, nil
}
func (nopChatRunner) EmitMessage(context.Context, string, string, json.RawMessage) (string, error) {
	return "", nil
}
func (runner *recordingChatRunner) Output(context.Context, string, loopd.OutputRequest) (loopd.Message, error) {
	return loopd.Message{}, nil
}
func (runner *recordingChatRunner) EmitMessage(context.Context, string, string, json.RawMessage) (string, error) {
	return "", nil
}
