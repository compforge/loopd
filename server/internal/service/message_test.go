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

	conversation, err := conversations.CreateConversation(ctx, "Planning", "")
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

	conversation, err := conversations.CreateConversation(ctx, "Planning", "")
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
	chat := NewChatService(nil, tasks, streams, nil)

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
	chat := NewChatService(nil, tasks, streams, nil)

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
	chat := NewChatService(nil, tasks, streams, nil)

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

func TestDetailConversationReferencesActorMessage(t *testing.T) {
	store := openServiceStore(t)
	conversations := NewConversationService(store, nil)
	messages := NewMessageService(store, nil)
	chat := NewChatService(store, nopTaskClient{}, nopChatRunner{}, nil)
	ctx := context.Background()

	root, err := conversations.CreateConversation(ctx, "Root", "")
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
	store, err := repo.Open(repo.Config{SQLitePath: filepath.Join(t.TempDir(), "loopd.db")})
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
