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

func TestChatCreatesVisibleMessagesWithOneTask(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	tasks := &recordingTaskClient{}
	chat := NewChatService(store, tasks, nopChatRunner{}, nil)
	ctx := context.Background()

	conversation, err := conversations.CreateConversation(ctx, "Planning", "user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := chat.Create(ctx, conversation.ID, "user-1", loopd.ActorRef{
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
	if tasks.createdTaskID != answer.TaskID || tasks.createdTarget != (loopd.ActorRef{
		Kind: loopd.RoleHarness,
		Key:  "harness-1",
	}) {
		t.Fatalf("created Task CRD = %q %#v", tasks.createdTaskID, tasks.createdTarget)
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

}

func TestChatRollsBackMessagesWhenTaskCreationFails(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	streams := &recordingChatRunner{}
	chat := NewChatService(store, &recordingTaskClient{createErr: errors.New("api unavailable")}, streams, nil)
	ctx := context.Background()

	conversation, err := conversations.CreateConversation(ctx, "Planning", "user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = chat.Create(ctx, conversation.ID, "user-1", loopd.ActorRef{
		Kind: loopd.RoleOperator,
		Key:  "operator-1",
	}, textContent("question"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Create error = %v, want %v", err, ErrUnavailable)
	}
	history, err := messages.ListMessages(ctx, conversation.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("Task creation failure left messages: %#v", history)
	}
	if streams.initializedTaskID == "" || streams.deletedTaskID != streams.initializedTaskID {
		t.Fatalf("initialized stream %q, deleted stream %q", streams.initializedTaskID, streams.deletedTaskID)
	}
}

func TestChatDeletesTaskWhenDatabaseCommitFails(t *testing.T) {
	tasks := &recordingTaskClient{}
	streams := &recordingChatRunner{}
	chat := NewChatService(failingCommitRepository{}, tasks, streams, nil)

	_, err := chat.Create(context.Background(), "conversation-1", "user-1", loopd.ActorRef{
		Kind: loopd.RoleOperator,
		Key:  "operator-1",
	}, textContent("question"))
	if err == nil {
		t.Fatal("Create succeeded when database commit failed")
	}
	if tasks.createdTaskID == "" || tasks.deletedTaskID != tasks.createdTaskID {
		t.Fatalf("created Task %q, deleted Task %q", tasks.createdTaskID, tasks.deletedTaskID)
	}
	if streams.initializedTaskID == "" || streams.deletedTaskID != streams.initializedTaskID {
		t.Fatalf("initialized stream %q, deleted stream %q", streams.initializedTaskID, streams.deletedTaskID)
	}
}

func TestChatCompleteRetiresTaskMarker(t *testing.T) {
	tasks := &recordingTaskClient{}
	streams := &recordingChatRunner{}
	chat := NewChatService(completionStore(t), tasks, streams, nil)

	if err := chat.Complete(context.Background(), "task-1", nil); err != nil {
		t.Fatal(err)
	}
	if streams.completedTaskID != "task-1" || tasks.deletedTaskID != "task-1" {
		t.Fatalf("completed stream %q, deleted Task %q", streams.completedTaskID, tasks.deletedTaskID)
	}
}

func TestChatCompleteKeepsTaskWhenDeliveryFails(t *testing.T) {
	tasks := &recordingTaskClient{}
	streams := &recordingChatRunner{completeErr: errors.New("redis unavailable")}
	chat := NewChatService(completionStore(t), tasks, streams, nil)

	if err := chat.Complete(context.Background(), "task-1", nil); err == nil {
		t.Fatal("Complete succeeded when delivery failed")
	}
	if tasks.deletedTaskID != "" {
		t.Fatalf("delivery failure deleted Task %q", tasks.deletedTaskID)
	}
}

func TestChatCompleteReportsTaskRetirementFailure(t *testing.T) {
	tasks := &recordingTaskClient{deleteErr: errors.New("api unavailable")}
	streams := &recordingChatRunner{}
	chat := NewChatService(completionStore(t), tasks, streams, nil)

	err := chat.Complete(context.Background(), "task-1", nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Complete error = %v, want %v", err, ErrUnavailable)
	}
	if streams.completedTaskID != "task-1" || tasks.deletedTaskID != "task-1" {
		t.Fatalf("completed stream %q, attempted Task delete %q", streams.completedTaskID, tasks.deletedTaskID)
	}

	tasks.deleteErr = nil
	if err := chat.Complete(context.Background(), "task-1", nil); err != nil {
		t.Fatalf("retry Complete: %v", err)
	}
}

func TestConversationOwnershipAndTaskScope(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nopTaskClient{}, nopChatRunner{}, nil)
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

type nopTaskClient struct{}

func (nopTaskClient) Create(context.Context, string, loopd.ActorRef) error { return nil }
func (nopTaskClient) Delete(context.Context, string) error                 { return nil }

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
}

func (runner *recordingChatRunner) Initialize(_ context.Context, taskID string, _ json.RawMessage) error {
	runner.initializedTaskID = taskID
	return nil
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

type recordingTaskClient struct {
	createdTaskID string
	createdTarget loopd.ActorRef
	deletedTaskID string
	createErr     error
	deleteErr     error
}

func (client *recordingTaskClient) Create(_ context.Context, taskID string, target loopd.ActorRef) error {
	client.createdTaskID = taskID
	client.createdTarget = target
	return client.createErr
}

func (client *recordingTaskClient) Delete(_ context.Context, taskID string) error {
	client.deletedTaskID = taskID
	return client.deleteErr
}

type failingCommitRepository struct{}

func (failingCommitRepository) CreateChatMessages(
	ctx context.Context,
	_ model.Message,
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
