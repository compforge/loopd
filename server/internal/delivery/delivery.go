// Package delivery multiplexes message-owned AgentUE streams for one Task.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

var ErrInvalidEvent = errors.New("invalid AgentUE event")

type MessageRepository interface {
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	ListMessagesByTask(context.Context, string, string, int) ([]model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
	EnsureOutput(context.Context, string, loopd.OutputRequest) (model.Message, error)
	SaveOutput(context.Context, string, []byte, uint64) error
	ObserveMessageActivity(context.Context, string, time.Time) error
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Event struct {
	MessageID string
	Message   *loopd.Message
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
func (coordinator *Coordinator) Initialize(ctx context.Context, taskID string, content json.RawMessage) error {
	start, err := agentueui.Start(content, 1)
	if err != nil {
		return err
	}
	data, err := start.Marshal()
	if err != nil {
		return err
	}
	return coordinator.events.Initialize(ctx, taskID, content, data, start.Seq)
}
func (coordinator *Coordinator) Delete(ctx context.Context, taskID string) error {
	return coordinator.events.Delete(ctx, taskID)
}

// Output creates an addressable message before any stream content is published.
func (coordinator *Coordinator) Output(ctx context.Context, taskID string, request loopd.OutputRequest) (loopd.Message, error) {
	if strings.TrimSpace(request.Key) == "" || len(request.Key) > 128 || !request.Actor.ValidTarget() {
		return loopd.Message{}, fmt.Errorf("%w: output key and actor are required", ErrInvalidEvent)
	}
	message, err := coordinator.repo.EnsureOutput(ctx, taskID, request)
	if err != nil {
		return loopd.Message{}, err
	}
	return visibleMessage(message), nil
}

// Emit is the main-answer convenience path; block content never determines its destination.
func (coordinator *Coordinator) Emit(ctx context.Context, taskID string, data json.RawMessage) (string, error) {
	response, err := coordinator.response(ctx, taskID)
	if err != nil {
		return "", err
	}
	return coordinator.EmitMessage(ctx, taskID, response.ID, data)
}

// +spec=`Message ID 决定输出归属，block ID 与 seq 只在该 Message 内唯一；Human 状态只能经 typed action 写入`
func (coordinator *Coordinator) EmitMessage(ctx context.Context, taskID, messageID string, data json.RawMessage) (string, error) {
	message, err := coordinator.repo.GetMessage(ctx, messageID)
	if err != nil {
		return "", err
	}
	if message.TaskID != taskID {
		return "", repo.ErrNotFound
	}
	if message.Purpose != "response" && message.Purpose != "output" {
		return "", fmt.Errorf("%w: message is not an output", ErrInvalidEvent)
	}
	response, err := coordinator.response(ctx, taskID)
	if err != nil {
		return "", err
	}
	if response.DeliveryState != "" {
		return "", repo.ErrConflict
	}
	event, err := agentueui.Parse(data)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if t, _ := event.Block["type"].(string); t == "ask" || t == "confirm" || t == "human_reply" {
		return "", fmt.Errorf("%w: Human blocks require typed actions", ErrInvalidEvent)
	}
	if _, exists := event.Meta["human"]; exists {
		return "", fmt.Errorf("%w: reserved Human metadata", ErrInvalidEvent)
	}
	if event.Op != agentueui.OpSet && event.Op != agentueui.OpAppend {
		return "", fmt.Errorf("%w: only set and append events may be emitted", ErrInvalidEvent)
	}
	if err := coordinator.ensureStream(ctx, message); err != nil {
		return "", err
	}
	id, err := coordinator.events.Publish(ctx, streamKey(message), data, event.Seq)
	if err == nil && message.Purpose == "output" && event.Timestamp != nil {
		err = coordinator.repo.ObserveMessageActivity(ctx, message.ID, time.UnixMilli(*event.Timestamp).UTC())
	}
	return id, err
}

// The main-answer stream retains the Task transport cursor. Additional messages
// own independent bridge keys and are replayed independently on reconnect.
func streamKey(message model.Message) string {
	if message.Purpose == "response" {
		return message.TaskID
	}
	return "message/" + message.ID
}
func (coordinator *Coordinator) ensureStream(ctx context.Context, message model.Message) error {
	key := streamKey(message)
	if _, err := coordinator.events.State(ctx, key); err == nil {
		return nil
	} else if !errors.Is(err, agentuerunner.ErrNotFound) {
		return err
	}
	revision := message.Revision
	if revision == 0 {
		revision = 1
	}
	start, err := agentueui.Start(message.Content, revision)
	if err != nil {
		return err
	}
	data, err := start.Marshal()
	if err != nil {
		return err
	}
	err = coordinator.events.Initialize(ctx, key, message.Content, data, revision)
	if errors.Is(err, agentuerunner.ErrConflict) {
		return nil
	}
	return err
}

// Complete persists all independently addressed outputs before ending the main
// stream. Ending a message is not permission to retire its Task.
func (coordinator *Coordinator) Complete(ctx context.Context, taskID string, failure *Failure) error {
	response, err := coordinator.response(ctx, taskID)
	if err != nil {
		return err
	}
	rows, err := coordinator.repo.ListMessagesByTask(ctx, taskID, "", -1)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Purpose == "output" {
			if err := coordinator.finishMessage(ctx, row, nil); err != nil {
				return err
			}
		}
	}
	return coordinator.finishMessage(ctx, response, failure)
}

func (coordinator *Coordinator) response(ctx context.Context, taskID string) (model.Message, error) {
	rows, err := coordinator.repo.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return model.Message{}, err
	}
	_, response, err := repo.TaskPair(rows)
	return response, err
}

func visibleMessage(m model.Message) loopd.Message {
	return loopd.Message{ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, Kind: loopd.Role(m.Kind), Key: m.ActorKey, Content: m.Content, ReplyToMessageID: m.ReplyToMessageID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: loopd.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}
