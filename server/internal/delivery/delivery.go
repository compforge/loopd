// Package delivery multiplexes message-owned AgentUE streams for a conversation.
package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
)

var ErrInvalidEvent = errors.New("invalid AgentUE event")

type MessageRepository interface {
	ProjectOutput(context.Context, string, agentueui.Event) error
	GetDeliveryInput(context.Context, string) (model.Message, error)
	ListDeliveryMessages(context.Context, string) ([]model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
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

// +spec=`Message ID 决定输出归属，block ID 与 seq 只在该 Message 内唯一；Human 状态只能经 typed action 写入`
func (coordinator *Coordinator) EmitMessage(ctx context.Context, messageID string, data json.RawMessage) (string, error) {
	message, err := coordinator.repo.GetMessage(ctx, messageID)
	if err != nil {
		return "", err
	}
	if message.Purpose != "output" {
		return "", fmt.Errorf("%w: message is not an output", ErrInvalidEvent)
	}
	event, err := parseOutputEvent(data)
	if err != nil {
		return "", err
	}
	if visibleMessage(message).Ended() {
		if event.Op == agentueui.OpEnd {
			return "", nil
		}
		return "", fmt.Errorf("%w: message has ended", ErrInvalidEvent)
	}
	// Each update extends one durable revision. A gap cannot be interpreted
	// safely as a delta or an End; the writer must retry its missing update.
	if event.Seq > message.Revision+1 {
		return "", fmt.Errorf("%w: event skips message revision", ErrInvalidEvent)
	}
	if err := coordinator.repo.ProjectOutput(ctx, message.ID, event); err != nil {
		return "", err
	}
	message, err = coordinator.repo.GetMessage(ctx, messageID)
	if err != nil {
		return "", err
	}
	id, err := coordinator.publish(ctx, message, event)
	if err != nil {
		// DB acceptance is the publication contract. A page bridge outage must
		// not make actors repeat business work; subscriptions repair from SQL.
		coordinator.logger.WarnContext(ctx, "page delivery deferred", "message_id", messageID, "error", err)
		return "", nil
	}
	if event.Op == agentueui.OpEnd {
		coordinator.logger.InfoContext(ctx, "message output ended", "message_id", messageID, "conversation_id", message.ConversationID)
	}
	return id, nil
}

func (coordinator *Coordinator) publish(ctx context.Context, message model.Message, event agentueui.Event) (string, error) {
	key := streamKey(message)
	state, err := coordinator.events.State(ctx, key)
	if errors.Is(err, agentuerunner.ErrNotFound) {
		if err := coordinator.ensureStream(ctx, message); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if state.LastSeq < message.Revision {
		// A skipped delivery or an out-of-order publisher needs a full snapshot,
		// not a delta whose predecessor never reached this bridge.
		if state.LastSeq+1 != event.Seq || message.Revision != event.Seq {
			event, err = agentueui.Start(message.Content, message.Revision)
			if err != nil {
				return "", err
			}
		}
		data, err := event.Marshal()
		if err != nil {
			return "", err
		}
		id, err := coordinator.events.Publish(ctx, key, data, event.Seq)
		if err != nil {
			return "", err
		}
		if !visibleMessage(message).Ended() {
			return id, nil
		}
	}
	if visibleMessage(message).Ended() {
		return "", coordinator.events.MarkTerminal(ctx, key, agentuerunner.StatusCompleted)
	}
	return "", nil
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

func (coordinator *Coordinator) input(ctx context.Context, taskID string) (model.Message, error) {
	return coordinator.repo.GetDeliveryInput(ctx, taskID)
}

func transportMessage(input model.Message) model.Message {
	return model.Message{TaskID: input.TaskID, ConversationID: input.ConversationID, Purpose: "transport", Revision: 1,
		Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)}
}

func visibleMessage(m model.Message) loopd.Message {
	return loopd.Message{TargetKind: loopd.ActorKind(m.TargetKind), TargetKey: m.TargetKey, ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, Kind: loopd.ActorKind(m.Kind), Key: m.ActorKey, Content: m.Content, ReplyToID: m.ReplyToID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: loopd.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}

func parseOutputEvent(data json.RawMessage) (agentueui.Event, error) {
	event, err := agentueui.Parse(data)
	if err != nil {
		return agentueui.Event{}, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if t, _ := event.Block["type"].(string); t == "ask" || t == "confirm" || t == "human_reply" {
		return agentueui.Event{}, fmt.Errorf("%w: Human blocks require typed actions", ErrInvalidEvent)
	}
	if event.Mask == "meta.output" || strings.HasPrefix(event.Mask, "meta.output.") || event.Mask == "meta.human" || strings.HasPrefix(event.Mask, "meta.human.") {
		return agentueui.Event{}, fmt.Errorf("%w: reserved message metadata", ErrInvalidEvent)
	}
	if _, exists := event.Meta["output"]; exists {
		return agentueui.Event{}, fmt.Errorf("%w: reserved output metadata", ErrInvalidEvent)
	}
	if _, exists := event.Meta["human"]; exists {
		return agentueui.Event{}, fmt.Errorf("%w: reserved Human metadata", ErrInvalidEvent)
	}
	if event.Op != agentueui.OpSet && event.Op != agentueui.OpAppend && event.Op != agentueui.OpEnd {
		return agentueui.Event{}, fmt.Errorf("%w: only set, append and end events may be emitted", ErrInvalidEvent)
	}
	return event, nil
}
