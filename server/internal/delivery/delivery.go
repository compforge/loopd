// Package delivery multiplexes message-owned AgentUE streams for one Task.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
)

var ErrInvalidEvent = errors.New("invalid AgentUE event")

type MessageRepository interface {
	ProjectOutput(context.Context, string, agentueui.Event) error
	GetDeliveryInput(context.Context, string) (model.Message, error)
	ListMessagesByTask(context.Context, string, string, int) ([]model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
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

// Emit is the main-answer convenience path; block content never determines its destination.

// +spec=`Message ID 决定输出归属，block ID 与 seq 只在该 Message 内唯一；Human 状态只能经 typed action 写入`
func (coordinator *Coordinator) EmitMessage(ctx context.Context, messageID string, data json.RawMessage) (string, error) {
	message, err := coordinator.repo.GetMessage(ctx, messageID)
	if err != nil {
		return "", err
	}
	if message.Purpose != "output" {
		return "", fmt.Errorf("%w: message is not an output", ErrInvalidEvent)
	}
	{
		event, err := agentueui.Parse(data)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidEvent, err)
		}
		if event.Op == agentueui.OpEnd {
			return "", coordinator.finishMessage(ctx, message, nil)
		}
	}
	event, err := parseOutputEvent(data)
	if err != nil {
		return "", err
	}
	if err := coordinator.ensureStream(ctx, message); err != nil {
		return "", err
	}
	id, err := coordinator.events.Publish(ctx, streamKey(message), data, event.Seq)
	if err == nil {
		err = coordinator.repo.ProjectOutput(ctx, message.ID, event)
	}
	if err == nil && message.Purpose == "output" && event.Timestamp != nil {
		err = coordinator.repo.ObserveMessageActivity(ctx, message.ID, time.UnixMilli(*event.Timestamp).UTC())
	}
	return id, err
}

// Only transport control owns the Chat cursor. Every actual Message has an
// independent bridge key.
func streamKey(message model.Message) string {
	if message.Purpose == "transport" {
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

// Complete ends only the
// UI transport. It never creates an answer to carry lifecycle state.
func (coordinator *Coordinator) Complete(ctx context.Context, taskID string, failure *Failure) error {
	input, err := coordinator.input(ctx, taskID)
	if err != nil {
		return err
	}
	control := transportMessage(input)
	if err := coordinator.ensureStream(ctx, control); err != nil {
		return err
	}
	state, err := coordinator.events.State(ctx, taskID)
	if err != nil {
		return err
	}
	if state.Status.Terminal() {
		return nil
	}
	seq, status := uint64(2), agentuerunner.StatusCompleted
	if failure != nil {
		status = agentuerunner.StatusFailed
		data, err := agentueui.Failure(seq, failure.Code, failure.Message).Marshal()
		if err != nil {
			return err
		}
		if _, err := coordinator.events.Publish(ctx, taskID, data, seq); err != nil {
			return err
		}
		seq++
	}
	data, err := agentueui.End(seq).Marshal()
	if err != nil {
		return err
	}
	if _, err := coordinator.events.Publish(ctx, taskID, data, seq); err != nil {
		return err
	}
	return coordinator.events.MarkTerminal(ctx, taskID, status)
}

func (coordinator *Coordinator) input(ctx context.Context, taskID string) (model.Message, error) {
	return coordinator.repo.GetDeliveryInput(ctx, taskID)
}

func transportMessage(input model.Message) model.Message {
	return model.Message{TaskID: input.TaskID, ConversationID: input.ConversationID, Purpose: "transport", Revision: 1,
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)}
}

func visibleMessage(m model.Message) loopd.Message {
	return loopd.Message{DeliveryState: m.DeliveryState, TargetKind: loopd.Role(m.TargetKind), TargetKey: m.TargetKey, ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, Kind: loopd.Role(m.Kind), Key: m.ActorKey, Content: m.Content, ReplyToID: m.ReplyToID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: loopd.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}

func parseOutputEvent(data json.RawMessage) (agentueui.Event, error) {
	event, err := agentueui.Parse(data)
	if err != nil {
		return agentueui.Event{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if t, _ := event.Block["type"].(string); t == "ask" || t == "confirm" || t == "human_reply" {
		return agentueui.Event{}, fmt.Errorf("%w: Human blocks require typed actions", ErrInvalidEvent)
	}
	if _, exists := event.Meta["human"]; exists {
		return agentueui.Event{}, fmt.Errorf("%w: reserved Human metadata", ErrInvalidEvent)
	}
	if event.Op != agentueui.OpSet && event.Op != agentueui.OpAppend {
		return agentueui.Event{}, fmt.Errorf("%w: only set and append events may be emitted", ErrInvalidEvent)
	}
	return event, nil
}
