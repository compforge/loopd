package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	loopd "github.com/compforge/loopd"
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
func (service *MessageService) Speak(ctx context.Context, convID string, request loopd.SpeakRequest) (loopd.Message, error) {
	if !request.Actor.ValidTarget() || strings.TrimSpace(request.Key) == "" ||
		(request.Target != (loopd.ActorRef{}) && (!request.Target.Kind.Valid() || request.Target.Key == "")) {
		return loopd.Message{}, ErrInvalid
	}
	if len(request.Content) == 0 {
		request.Content = []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	}
	if validateContent(request.Content) != nil {
		return loopd.Message{}, ErrInvalid
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
	kind loopd.ActorKind,
	key string,
	content json.RawMessage,
) (loopd.Message, error) {
	if strings.TrimSpace(taskID) == "" || !kind.Valid() || strings.TrimSpace(key) == "" || validateContent(content) != nil {
		return loopd.Message{}, ErrInvalid
	}
	message, err := service.repo.CreateMessage(ctx, model.Message{
		ID:             uuid.V7(),
		ConversationID: conversationID,
		TaskID:         strings.TrimSpace(taskID),
		Kind:           string(kind),
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
) ([]loopd.Message, error) {
	limit = pageSize(limit)
	rows, err := service.repo.ListMessages(ctx, conversationID, after, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]loopd.Message, 0, len(rows))
	for _, row := range rows {
		messages = append(messages, messageFromModel(row))
	}
	return messages, nil
}

func messageFromModel(value model.Message) loopd.Message {
	return loopd.Message{
		TargetKind: loopd.ActorKind(value.TargetKind), TargetKey: value.TargetKey,
		ReplyToID: value.ReplyToID, Purpose: value.Purpose, Revision: value.Revision,
		ID: value.ID, ConversationID: value.ConversationID, TaskID: value.TaskID,
		Kind: loopd.ActorKind(value.Kind), Key: value.ActorKey, Content: json.RawMessage(value.Content),
		Timestamped: loopd.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
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
	var model struct {
		Version string            `json:"version"`
		Biz     string            `json:"biz"`
		Meta    json.RawMessage   `json:"meta"`
		Blocks  []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(content, &model); err != nil {
		return err
	}
	if model.Version == "" || model.Biz == "" || len(model.Meta) == 0 || model.Blocks == nil {
		return ErrInvalid
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(model.Meta, &meta); err != nil {
		return err
	}
	if _, reserved := meta["output"]; reserved {
		return ErrInvalid
	}
	if _, reserved := meta["human"]; reserved {
		return ErrInvalid
	}
	blockIDs := make(map[string]struct{}, len(model.Blocks))
	for _, rawBlock := range model.Blocks {
		var block struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return err
		}
		if block.Type == "ask" || block.Type == "confirm" || block.Type == "human_reply" {
			return ErrInvalid
		}
		if block.ID == "" || block.Type == "" {
			return ErrInvalid
		}
		if _, exists := blockIDs[block.ID]; exists {
			return ErrInvalid
		}
		blockIDs[block.ID] = struct{}{}
	}
	return nil
}
