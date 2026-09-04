package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

type ChatRepository interface {
	CreateChatMessages(context.Context, model.Message, model.Message, func(context.Context) error) (model.Message, error)
}

type TaskMarker interface {
	Create(context.Context, string, loopd.ResponderRef) error
	Delete(context.Context, string) error
}

// ChatService owns the transaction boundary for one user question. It writes
// the visible message pair, creates the same-ID Task CRD before commit, and
// returns the responder message that will be updated as work progresses.
type ChatService struct {
	repo       ChatRepository
	taskMarker TaskMarker
	logger     *slog.Logger
}

func NewChatService(repository ChatRepository, taskMarker TaskMarker, logger *slog.Logger) *ChatService {
	return &ChatService{repo: repository, taskMarker: taskMarker, logger: loggerOrDefault(logger)}
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
	if service.taskMarker == nil {
		return loopd.Message{}, ErrUnavailable
	}

	taskID := uuid.V7()
	responderContent, err := emptyContent(content)
	if err != nil {
		return loopd.Message{}, ErrInvalid
	}
	taskCreated := false
	message, err := service.repo.CreateChatMessages(ctx,
		model.Message{
			ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
			Kind: string(loopd.RoleUser), Key: userKey, Content: content,
		},
		model.Message{
			ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
			Kind: string(responder.Kind), Key: responder.Key, Content: responderContent,
		},
		func(txCtx context.Context) error {
			if err := service.taskMarker.Create(txCtx, taskID, responder); err != nil {
				service.logger.ErrorContext(ctx, "create task marker failed",
					"conversation_id", conversationID,
					"task_id", taskID,
					"responder_kind", responder.Kind,
					"responder_key", responder.Key,
					"error", err,
				)
				return fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			taskCreated = true
			return nil
		},
	)
	// Kubernetes and the database do not share a transaction coordinator. If
	// the CRD was created but the database commit fails, compensate so an
	// Operator cannot later reconcile a task whose visible chat state vanished.
	if err != nil && taskCreated {
		if cleanupErr := service.taskMarker.Delete(context.WithoutCancel(ctx), taskID); cleanupErr != nil {
			service.logger.ErrorContext(ctx, "delete rolled back task marker failed",
				"conversation_id", conversationID,
				"task_id", taskID,
				"error", cleanupErr,
			)
		} else {
			service.logger.InfoContext(ctx, "rolled back task marker",
				"conversation_id", conversationID,
				"task_id", taskID,
			)
		}
	}
	if err == nil {
		service.logger.InfoContext(ctx, "chat task created",
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
