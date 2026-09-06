package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

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
	repo   repo.MessageRepository
	logger *slog.Logger
}

func NewMessageService(repository repo.MessageRepository, logger *slog.Logger) *MessageService {
	return &MessageService{repo: repository, logger: loggerOrDefault(logger)}
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
