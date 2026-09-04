// Package delivery connects loop-server business completion to AgentUE delivery.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

var ErrInvalidEvent = errors.New("invalid AgentUE event")

type MessageRepository interface {
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	UpdateMessageContent(context.Context, string, string, []byte) (model.Message, error)
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Event struct {
	ID        string
	Data      json.RawMessage
	Persisted bool
}

type Coordinator struct {
	events agentuerunner.EventBridge
	repo   MessageRepository
	logger *slog.Logger
}

func New(events agentuerunner.EventBridge, repository MessageRepository, logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{events: events, repo: repository, logger: logger}
}

func (coordinator *Coordinator) Initialize(ctx context.Context, taskID string, model json.RawMessage) error {
	start, err := agentueui.Start(model, 1)
	if err != nil {
		return err
	}
	data, err := start.Marshal()
	if err != nil {
		return err
	}
	return coordinator.events.Initialize(ctx, taskID, model, data, start.Seq)
}

func (coordinator *Coordinator) Delete(ctx context.Context, taskID string) error {
	return coordinator.events.Delete(ctx, taskID)
}

func (coordinator *Coordinator) Emit(ctx context.Context, taskID string, data json.RawMessage) (string, error) {
	event, err := agentueui.Parse(data)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if event.Op != agentueui.OpSet && event.Op != agentueui.OpAppend {
		return "", fmt.Errorf("%w: only set and append events may be emitted", ErrInvalidEvent)
	}
	return coordinator.events.Publish(ctx, taskID, data, event.Seq)
}

func (coordinator *Coordinator) Complete(ctx context.Context, taskID string, failure *Failure) error {
	state, err := coordinator.events.State(ctx, taskID)
	if err != nil {
		return err
	}
	if state.Status.Terminal() {
		return nil
	}
	conversationID, responseMessageID, err := coordinator.responseMessage(ctx, taskID)
	if err != nil {
		return err
	}
	values, err := coordinator.events.EventsThrough(ctx, taskID, "")
	if err != nil {
		return err
	}
	snapshot := map[string]any{}
	lastSeq := uint64(0)
	lastOp := agentueui.Op("")
	hasFailure := false
	for _, value := range values {
		event, parseErr := agentueui.Parse(value.Data)
		if parseErr != nil {
			return fmt.Errorf("rebuild task %q at cursor %q: %w", taskID, value.Cursor, parseErr)
		}
		if event.Op != agentueui.OpPing && event.Seq <= lastSeq {
			return fmt.Errorf("task %q AgentUE sequence did not increase", taskID)
		}
		snapshot, err = agentueui.Apply(snapshot, event)
		if err != nil {
			return fmt.Errorf("rebuild task %q at cursor %q: %w", taskID, value.Cursor, err)
		}
		if event.Op != agentueui.OpPing {
			lastSeq = event.Seq
		}
		if event.Op == agentueui.OpError {
			hasFailure = true
		}
		lastOp = event.Op
	}
	status := agentuerunner.StatusCompleted
	if failure != nil && lastOp != agentueui.OpEnd && !hasFailure {
		status = agentuerunner.StatusFailed
		lastSeq++
		failed := agentueui.Failure(lastSeq, failure.Code, failure.Message)
		data, marshalErr := failed.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = coordinator.events.Publish(ctx, taskID, data, failed.Seq); err != nil {
			return err
		}
		snapshot, err = agentueui.Apply(snapshot, failed)
		if err != nil {
			return err
		}
		hasFailure = true
		lastOp = failed.Op
	}
	if hasFailure {
		status = agentuerunner.StatusFailed
	}
	content, err := agentueui.MarshalSnapshot(snapshot)
	if err != nil {
		return fmt.Errorf("marshal task %q snapshot: %w", taskID, err)
	}
	if _, err := coordinator.repo.UpdateMessageContent(ctx, conversationID, responseMessageID, content); err != nil {
		return fmt.Errorf("persist task %q snapshot: %w", taskID, err)
	}
	if lastOp != agentueui.OpEnd {
		lastSeq++
		end := agentueui.End(lastSeq)
		data, marshalErr := end.Marshal()
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = coordinator.events.Publish(ctx, taskID, data, end.Seq); err != nil {
			return err
		}
	}
	if err := coordinator.events.MarkTerminal(ctx, taskID, status); err != nil {
		return err
	}
	coordinator.logger.InfoContext(ctx, "chat task completed",
		"task_id", taskID,
		"conversation_id", conversationID,
		"message_id", responseMessageID,
		"status", status,
	)
	return nil
}

func (coordinator *Coordinator) Stream(
	ctx context.Context,
	taskID string,
	conversationID string,
	after string,
	deliver func(Event) error,
) error {
	taskConversationID, _, err := coordinator.responseMessage(ctx, taskID)
	if err != nil {
		return err
	}
	if taskConversationID != conversationID {
		return agentuerunner.ErrNotFound
	}
	replayer := agentuerunner.Replayer{Bridge: coordinator.events}
	return replayer.Stream(ctx, taskID, after, func(event agentuerunner.Delivery) error {
		return deliver(Event{ID: event.Cursor, Data: event.Data, Persisted: event.Cursor != ""})
	})
}

func (coordinator *Coordinator) responseMessage(ctx context.Context, taskID string) (string, string, error) {
	rows, err := coordinator.repo.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return "", "", err
	}
	var conversationID, responseMessageID string
	hasUser := false
	for _, row := range rows {
		if conversationID != "" && conversationID != row.ConversationID {
			return "", "", repo.ErrNotFound
		}
		conversationID = row.ConversationID
		if row.Kind == string(loopd.RoleUser) {
			hasUser = true
			continue
		}
		if responseMessageID != "" {
			return "", "", repo.ErrNotFound
		}
		responseMessageID = row.ID
	}
	if !hasUser || conversationID == "" || responseMessageID == "" {
		return "", "", repo.ErrNotFound
	}
	return conversationID, responseMessageID, nil
}
