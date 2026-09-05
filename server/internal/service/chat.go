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
	BeginCompletion(context.Context, string, []byte) error
	FinishCompletion(context.Context, string) error
	PendingCompletions(context.Context) ([]model.Message, error)
	CreateChatInput(context.Context, model.Message, func(context.Context) error) (model.Message, error)
}

type ChatDelivery interface {
	EmitMessage(context.Context, string, json.RawMessage) (string, error)
	Initialize(context.Context, string, json.RawMessage) error
	Delete(context.Context, string) error
	Complete(context.Context, string, *delivery.Failure) error
	Stream(context.Context, string, string, string, func(delivery.Event) error) error
}

// ChatService owns one UI chat delivery, not an Operator's business task.
// An input starts the delivery; answers are created only when actors publish.
type ChatService struct {
	notifier MessageNotifier
	repo     ChatRepository
	delivery ChatDelivery
	logger   *slog.Logger
}

type MessageNotifier interface {
	Notify(context.Context, model.Message) error
}

func NewChatService(repository ChatRepository, chatDelivery ChatDelivery, logger *slog.Logger, notifier MessageNotifier) *ChatService {
	return &ChatService{repo: repository, delivery: chatDelivery, logger: loggerOrDefault(logger), notifier: notifier}
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
	if service.delivery == nil {
		return loopd.Message{}, ErrUnavailable
	}
	taskID := uuid.V7()
	streamContent, err := emptyContent(content)
	if err != nil {
		return loopd.Message{}, ErrInvalid
	}
	input := model.Message{
		ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
		Kind: string(loopd.RoleUser), ActorKey: userKey, Content: content,
		TargetKind: string(target.Kind), TargetKey: target.Key, DispatchPending: true,
	}
	streamCreated := false
	message, err := service.repo.CreateChatInput(ctx, input, func(txCtx context.Context) error {
		if err := service.delivery.Initialize(txCtx, taskID, streamContent); err != nil {
			return fmt.Errorf("%w: initialize chat stream: %v", ErrUnavailable, err)
		}
		streamCreated = true
		return nil
	})
	if err != nil {
		if streamCreated {
			if cleanupErr := service.delivery.Delete(context.WithoutCancel(ctx), taskID); cleanupErr != nil {
				service.logger.ErrorContext(ctx, "delete uncommitted chat stream", "task_id", taskID, "error", cleanupErr)
			}
		}
		return loopd.Message{}, err
	}
	if service.notifier != nil {
		if err := service.notifier.Notify(ctx, message); err != nil {
			service.logger.WarnContext(ctx, "conversation notification pending",
				"conversation_id", conversationID, "message_id", message.ID, "error", err)
		}
	}
	service.logger.InfoContext(ctx, "chat input committed", "conversation_id", conversationID,
		"task_id", taskID, "message_id", message.ID, "target_kind", target.Kind, "target_key", target.Key)
	return messageFromModel(message), nil
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

func (service *ChatService) Complete(ctx context.Context, taskID string, failure *delivery.Failure) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ErrInvalid
	}
	intent, _ := json.Marshal(failure)
	if err := service.repo.BeginCompletion(ctx, taskID, intent); err != nil {
		return err
	}
	if err := mapDeliveryError(service.delivery.Complete(ctx, taskID, failure)); err != nil {
		return err
	}
	// Completing a UI stream never retires the conversation or business resources.
	if err := service.repo.FinishCompletion(ctx, taskID); err != nil {
		return err
	}
	service.logger.InfoContext(ctx, "chat delivery completed", "task_id", taskID)
	return nil
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

func (service *ChatService) resumeCompletion(ctx context.Context, taskID string, intent []byte) error {
	var failure *delivery.Failure
	if err := json.Unmarshal(intent, &failure); err != nil {
		return err
	}
	return service.Complete(ctx, taskID, failure)
}

func (service *ChatService) EmitMessage(ctx context.Context, messageID string, event json.RawMessage) (string, error) {
	id, err := service.delivery.EmitMessage(ctx, messageID, event)
	return id, mapDeliveryError(err)
}
