package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

const (
	defaultPageSize = 100
	maxPageSize     = 500
)

type MessageService struct {
	events agentuerunner.EventBridge
	repo   repo.MessageRepository
	logger *slog.Logger
}

func NewMessageService(repository repo.MessageRepository, events agentuerunner.EventBridge, logger *slog.Logger) *MessageService {
	return &MessageService{repo: repository, events: events, logger: loggerOrDefault(logger)}
}

// Speak is independent of user input and transport completion. Notification
// retries are carried by the message's existing dispatch marker.
func (service *MessageService) Speak(ctx context.Context, convID string, request contract.SpeakRequest) (contract.Message, error) {
	if !request.Actor.ValidTarget() || strings.TrimSpace(request.Key) == "" ||
		(request.Target != (contract.ActorRef{}) && (!request.Target.Kind.Valid() || request.Target.Key == "")) {
		return contract.Message{}, ErrInvalid
	}
	if len(request.Content) == 0 {
		request.Content = []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)
	}
	if validateContent(request.Content) != nil {
		return contract.Message{}, ErrInvalid
	}
	message, err := service.repo.Speak(ctx, convID, request)
	if err == nil {
		service.logger.InfoContext(ctx, "actor message published", "conversation_id", convID,
			"message_id", message.ID, "actor_kind", request.Actor.Kind, "actor_key", request.Actor.Key)
	}
	return messageFromModel(message), err
}

func (service *MessageService) CreateMessage(
	ctx context.Context,
	conversationID string,
	taskID string,
	kind contract.ActorKind,
	key string,
	content json.RawMessage,
) (contract.Message, error) {
	if strings.TrimSpace(taskID) == "" || !kind.Valid() || strings.TrimSpace(key) == "" || validateContent(content) != nil {
		return contract.Message{}, ErrInvalid
	}
	message, err := service.repo.CreateMessage(ctx, model.Message{
		ID:             uuid.V7(),
		ConversationID: conversationID,
		TaskID:         strings.TrimSpace(taskID),
		Kind:           kind,
		ActorKey:       strings.TrimSpace(key),
		Content:        content,
	})
	if err == nil {
		service.logger.InfoContext(ctx, "message created",
			"conversation_id", conversationID,
			"message_id", message.ID,
			"task_id", message.TaskID,
			"kind", message.Kind,
			"actor_key", message.ActorKey,
		)
	}
	return messageFromModel(message), err
}

func (service *MessageService) ListMessages(
	ctx context.Context,
	conversationID string,
	after string,
	limit int,
) ([]contract.Message, error) {
	limit = pageSize(limit)
	rows, err := service.repo.ListMessages(ctx, conversationID, after, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]contract.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, messageFromModel(row))
	}
	return messages, nil
}

func messageFromModel(value model.Message) contract.Message {
	return contract.Message{
		Status:     contract.MessageStatus(value.Status),
		TargetKind: value.TargetKind, TargetKey: value.TargetKey,
		ReplyToID: value.ReplyToID, Purpose: value.Purpose, Revision: value.Revision,
		ID: value.ID, ConversationID: value.ConversationID, TaskID: value.TaskID,
		Kind: value.Kind, Key: value.ActorKey, Content: json.RawMessage(value.Content),
		Timestamped: contract.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
	}
}

func pageSize(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}

func validateContent(content json.RawMessage) error {
	var snapshot map[string]any
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return err
	}
	if err := ui.ValidateModel(snapshot); err != nil {
		return err
	}
	meta := snapshot["meta"].(map[string]any)
	if _, reserved := meta["output"]; reserved {
		return ErrInvalid
	}
	if _, reserved := meta["human"]; reserved {
		return ErrInvalid
	}
	for _, value := range snapshot["blocks"].([]any) {
		block := value.(map[string]any)
		switch block["type"] {
		case "ask", "confirm", "human_reply":
			return ErrInvalid
		}
	}
	return nil
}

// MessageChanges checks small metadata first; unchanged bodies and Parts stay in DB.
func (service *MessageService) MessageChanges(ctx context.Context, convID string, revisions map[string]uint64) ([]contract.Message, error) {
	if len(revisions) > maxPageSize {
		return nil, ErrInvalid
	}
	ids := make([]string, 0, len(revisions))
	for id := range revisions {
		ids = append(ids, id)
	}
	states, err := service.repo.GetMessageStates(ctx, convID, ids)
	if err != nil {
		return nil, err
	}
	ids = ids[:0]
	for _, state := range states {
		if state.Revision > revisions[state.ID] {
			ids = append(ids, state.ID)
		}
	}
	rows, err := service.repo.GetMessages(ctx, convID, ids)
	if err != nil {
		return nil, err
	}
	result := make([]contract.Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, messageFromModel(row))
	}
	return result, nil
}

// EmitMessage accepts output once it is persisted; Redis delivery is best effort.
// It shares Message ownership with Speak, independently of any chat submission.
// +spec=`Message ID 决定输出归属，block ID 与 seq 只在该 Message 内唯一；Human 状态只能经 typed action 写入`
func (service *MessageService) EmitMessage(ctx context.Context, messageID string, data json.RawMessage, statuses ...contract.MessageStatus) (string, error) {
	message, err := service.repo.GetMessageState(ctx, messageID)
	if err != nil {
		return "", err
	}
	if message.Purpose != "output" {
		return "", fmt.Errorf("%w: message is not an output", ErrInvalid)
	}
	event, err := parseOutputEvent(data)
	if err != nil {
		return "", err
	}
	status := contract.MessageStatus("")
	if len(statuses) > 1 {
		return "", fmt.Errorf("%w: at most one message status", ErrInvalid)
	}
	if len(statuses) == 1 {
		status = statuses[0]
	}
	if event.Op == ui.OpEnd && status == "" {
		status = contract.MessageStatusCompleted
	}
	if (event.Op == ui.OpEnd && !status.Terminal()) || (event.Op != ui.OpEnd && status != "") {
		return "", fmt.Errorf("%w: only End accepts a terminal message status", ErrInvalid)
	}
	if message.Ended {
		if event.Op == ui.OpEnd {
			if status != message.Status {
				return "", repo.ErrConflict
			}
			return "", nil
		}
		return "", fmt.Errorf("%w: message has ended", ErrInvalid)
	}
	// Each update extends one durable revision. A gap cannot be interpreted
	// safely as a delta or an End; the writer must retry its missing update.
	if event.Seq > message.Revision+1 {
		return "", fmt.Errorf("%w: event skips message revision", ErrInvalid)
	}
	if err := service.repo.ProjectOutput(ctx, message.ID, event, status); err != nil {
		return "", err
	}
	message, err = service.repo.GetMessageState(ctx, messageID)
	if err != nil {
		return "", err
	}
	id, err := service.publish(ctx, message, event)
	if err != nil {
		// DB acceptance is the publication contract. A page bridge outage must
		// not make actors repeat business work; subscriptions repair from SQL.
		service.logger.WarnContext(ctx, "page delivery deferred", "message_id", messageID, "error", err)
		return "", nil
	}
	if event.Op == ui.OpEnd {
		service.logger.InfoContext(ctx, "message output ended", "message_id", messageID, "conversation_id", message.ConversationID, "status", message.Status)
	}
	return id, nil
}

func (service *MessageService) publish(ctx context.Context, message repo.MessageState, event ui.Event) (string, error) {
	key := "message/" + message.ID
	state, err := service.events.State(ctx, key)
	if errors.Is(err, agentuerunner.ErrNotFound) {
		if err := service.ensureStream(ctx, model.Message{ID: message.ID, Purpose: message.Purpose}); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else if state.LastSeq < message.Revision {
		// A skipped delivery or an out-of-order publisher needs a full snapshot,
		// not a delta whose predecessor never reached this bridge.
		if state.LastSeq+1 != event.Seq || message.Revision != event.Seq {
			snapshot, err := service.repo.GetMessage(ctx, message.ID)
			if err != nil {
				return "", err
			}
			message.Ended = messageFromModel(snapshot).Ended()
			event, err = ui.Start(snapshot.Content, snapshot.Revision)
			if err != nil {
				return "", err
			}
		}
		data, err := event.Marshal()
		if err != nil {
			return "", err
		}
		id, err := service.events.Publish(ctx, key, data, event.Seq)
		if err != nil {
			return "", err
		}
		if !message.Ended {
			return id, nil
		}
	}
	if message.Ended {
		return "", service.events.MarkTerminal(ctx, key, agentuerunner.StatusCompleted)
	}
	return "", nil
}

func streamKey(message model.Message) string { return "message/" + message.ID }
func (service *MessageService) ensureStream(ctx context.Context, message model.Message) error {
	key := streamKey(message)
	if _, err := service.events.State(ctx, key); err == nil {
		return nil
	} else if !errors.Is(err, agentuerunner.ErrNotFound) {
		return err
	}

	var err error
	message, err = service.repo.GetMessage(ctx, message.ID)
	if err != nil {
		return err
	}
	revision := message.Revision
	if revision == 0 {
		revision = 1
	}
	start, err := ui.Start(message.Content, revision)
	if err != nil {
		return err
	}
	data, err := start.Marshal()
	if err != nil {
		return err
	}
	err = service.events.Initialize(ctx, key, message.Content, data, revision)
	if errors.Is(err, agentuerunner.ErrConflict) {
		return nil
	}
	return err
}

func parseOutputEvent(data json.RawMessage) (ui.Event, error) {
	event, err := ui.Parse(data)
	if err != nil {
		return ui.Event{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if t, _ := event.Block["type"].(string); t == "ask" || t == "confirm" || t == "human_reply" {
		return ui.Event{}, fmt.Errorf("%w: Human blocks require typed actions", ErrInvalid)
	}
	if event.Mask == "meta.output" || strings.HasPrefix(event.Mask, "meta.output.") || event.Mask == "meta.human" || strings.HasPrefix(event.Mask, "meta.human.") {
		return ui.Event{}, fmt.Errorf("%w: reserved message metadata", ErrInvalid)
	}
	if _, exists := event.Meta["output"]; exists {
		return ui.Event{}, fmt.Errorf("%w: reserved output metadata", ErrInvalid)
	}
	if _, exists := event.Meta["human"]; exists {
		return ui.Event{}, fmt.Errorf("%w: reserved Human metadata", ErrInvalid)
	}
	if event.Op != ui.OpSet && event.Op != ui.OpAppend && event.Op != ui.OpEnd {
		return ui.Event{}, fmt.Errorf("%w: only set, append and end events may be emitted", ErrInvalid)
	}
	return event, nil
}

// PublishCommitted uses the same bridge as Operator output, after the fenced
// SQL transaction. Failure is repaired by snapshots, not by repeating execution.
func (c *MessageService) PublishCommitted(ctx context.Context, id string, event ui.Event) {
	state, err := c.repo.GetMessageState(ctx, id)
	if err == nil {
		// A completion transaction can commit result + end together. Deliver
		// each committed event at its own revision, preserving the Redis log.
		if event.Seq > state.Revision {
			return
		}
		state.Revision = event.Seq
		state.Ended = event.Op == ui.OpEnd
		_, err = c.publish(ctx, state, event)
	}
	if err != nil {
		c.logger.WarnContext(ctx, "Harness page delivery deferred", "message_id", id, "error", err)
	}
}
