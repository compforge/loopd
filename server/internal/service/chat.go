package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

type ChatRepository interface {
	CreateChatMessages(context.Context, model.Message, model.Message, func(context.Context) error) (model.Message, error)
}

type TaskClient interface {
	Create(context.Context, string, loopd.ActorRef) error
	Delete(context.Context, string) error
}

type ChatDelivery interface {
	Initialize(context.Context, string, json.RawMessage) error
	Delete(context.Context, string) error
	Emit(context.Context, string, json.RawMessage) (string, error)
	Complete(context.Context, string, *delivery.Failure) error
	Stream(context.Context, string, string, string, func(delivery.Event) error) error
}

// ChatService owns the transaction boundary for one user question. It writes
// the visible message pair, creates the same-ID Task CRD before commit, and
// returns the selected Actor's message that will be updated as work progresses.
type ChatService struct {
	repo     ChatRepository
	tasks    TaskClient
	delivery ChatDelivery
	logger   *slog.Logger
}

func NewChatService(repository ChatRepository, tasks TaskClient, chatDelivery ChatDelivery, logger *slog.Logger) *ChatService {
	return &ChatService{repo: repository, tasks: tasks, delivery: chatDelivery, logger: loggerOrDefault(logger)}
}

func (service *ChatService) Create(
	ctx context.Context,
	conversationID string,
	userKey string,
	target loopd.ActorRef,
	content json.RawMessage,
) (loopd.Message, error) {
	userKey = strings.TrimSpace(userKey)
	target.Key = strings.TrimSpace(target.Key)
	if userKey == "" || !target.ValidTarget() || validateContent(content) != nil {
		return loopd.Message{}, ErrInvalid
	}
	if service.tasks == nil || service.delivery == nil {
		return loopd.Message{}, ErrUnavailable
	}

	taskID := uuid.V7()
	responseContent, err := emptyContent(content)
	if err != nil {
		return loopd.Message{}, ErrInvalid
	}
	userMessage := model.Message{
		ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
		Kind: string(loopd.RoleUser), Key: userKey, Content: content,
	}
	responseMessage := model.Message{
		ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
		Kind: string(target.Kind), Key: target.Key, Content: responseContent,
	}
	taskCreated := false
	streamCreated := false
	message, err := service.repo.CreateChatMessages(ctx,
		userMessage,
		responseMessage,
		func(txCtx context.Context) error {
			if err := service.delivery.Initialize(txCtx, taskID, responseContent); err != nil {
				service.logger.ErrorContext(ctx, "initialize chat event stream failed",
					"conversation_id", conversationID,
					"task_id", taskID,
					"error", err,
				)
				return fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			streamCreated = true
			if err := service.tasks.Create(txCtx, taskID, target); err != nil {
				service.logger.ErrorContext(ctx, "create task CRD failed",
					"conversation_id", conversationID,
					"task_id", taskID,
					"target_kind", target.Kind,
					"target_key", target.Key,
					"error", err,
				)
				if cleanupErr := service.delivery.Delete(context.WithoutCancel(ctx), taskID); cleanupErr != nil {
					service.logger.ErrorContext(ctx, "delete rolled back chat event stream failed",
						"task_id", taskID,
						"error", cleanupErr,
					)
				}
				streamCreated = false
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
		if cleanupErr := service.tasks.Delete(context.WithoutCancel(ctx), taskID); cleanupErr != nil {
			service.logger.ErrorContext(ctx, "delete rolled back task CRD failed",
				"conversation_id", conversationID,
				"task_id", taskID,
				"error", cleanupErr,
			)
		} else {
			service.logger.InfoContext(ctx, "rolled back task CRD",
				"conversation_id", conversationID,
				"task_id", taskID,
			)
		}
	}
	if err != nil && streamCreated {
		if cleanupErr := service.delivery.Delete(context.WithoutCancel(ctx), taskID); cleanupErr != nil {
			service.logger.ErrorContext(ctx, "delete rolled back chat event stream failed",
				"task_id", taskID,
				"error", cleanupErr,
			)
		}
	}
	if err == nil {
		service.logger.InfoContext(ctx, "chat task created",
			"conversation_id", conversationID,
			"task_id", taskID,
			"actor_message_id", message.ID,
			"target_kind", target.Kind,
			"target_key", target.Key,
		)
	}
	return messageFromModel(message), err
}

func (service *ChatService) Stream(
	ctx context.Context,
	conversationID string,
	taskID string,
	after string,
	deliver func(delivery.Event) error,
) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ErrInvalid
	}
	service.logger.InfoContext(ctx, "chat stream opened",
		"conversation_id", conversationID,
		"task_id", taskID,
		"after", after,
	)
	err := mapDeliveryError(service.delivery.Stream(ctx, taskID, conversationID, after, deliver))
	service.logger.InfoContext(ctx, "chat stream closed",
		"conversation_id", conversationID,
		"task_id", taskID,
		"error", err,
	)
	return err
}

func (service *ChatService) Emit(ctx context.Context, taskID string, event json.RawMessage) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", ErrInvalid
	}
	cursor, err := service.delivery.Emit(ctx, taskID, event)
	if err == nil {
		service.logger.DebugContext(ctx, "chat event published", "task_id", taskID, "cursor", cursor)
	}
	return cursor, mapDeliveryError(err)
}

func (service *ChatService) Complete(ctx context.Context, taskID string, failure *delivery.Failure) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ErrInvalid
	}
	return mapDeliveryError(service.delivery.Complete(ctx, taskID, failure))
}

func mapDeliveryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, agentuerunner.ErrNotFound):
		return repo.ErrNotFound
	case errors.Is(err, agentuerunner.ErrConflict):
		return ErrConflict
	case errors.Is(err, delivery.ErrInvalidEvent):
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	default:
		return err
	}
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
