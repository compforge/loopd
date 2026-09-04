package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

type ChatRepository interface {
	CreateChatMessages(context.Context, model.Message, model.Message) (model.Message, error)
}

// ChatService owns the transaction boundary for one user question. It creates
// the visible user and responder messages together and returns the responder
// message that the operator or harness will update as work progresses.
type ChatService struct {
	repo   ChatRepository
	logger *slog.Logger
}

func NewChatService(repository ChatRepository, logger *slog.Logger) *ChatService {
	return &ChatService{repo: repository, logger: loggerOrDefault(logger)}
}

func (service *ChatService) Create(
	ctx context.Context,
	conversationID string,
	userKey string,
	responder loopd.ResponderRef,
	content json.RawMessage,
) (loopd.Message, error) {
	userKey = strings.TrimSpace(userKey)
	responder.Key = strings.TrimSpace(responder.Key)
	if userKey == "" || !responder.Valid() || validateContent(content) != nil {
		return loopd.Message{}, ErrInvalid
	}

	taskID := uuid.V7()
	responderContent, err := emptyContent(content)
	if err != nil {
		return loopd.Message{}, ErrInvalid
	}
	message, err := service.repo.CreateChatMessages(ctx,
		model.Message{
			ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
			Kind: string(loopd.RoleUser), Key: userKey, Content: content,
		},
		model.Message{
			ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
			Kind: string(responder.Kind), Key: responder.Key, Content: responderContent,
		},
	)
	if err == nil {
		service.logger.InfoContext(ctx, "chat messages created",
			"conversation_id", conversationID,
			"task_id", taskID,
			"responder_message_id", message.ID,
			"responder_kind", responder.Kind,
			"responder_key", responder.Key,
		)
	}
	return messageFromModel(message), err
}

func emptyContent(content json.RawMessage) (json.RawMessage, error) {
	var source struct {
		Version string `json:"version"`
		Biz     string `json:"biz"`
	}
	if err := json.Unmarshal(content, &source); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version string            `json:"version"`
		Biz     string            `json:"biz"`
		Meta    map[string]any    `json:"meta"`
		Blocks  []json.RawMessage `json:"blocks"`
	}{Version: source.Version, Biz: source.Biz, Meta: map[string]any{}, Blocks: []json.RawMessage{}})
}
