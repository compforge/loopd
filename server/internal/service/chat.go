package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

type ChatRepository interface {
	CreateChatInput(context.Context, model.Message) (model.Message, error)
}

type ChatDelivery interface {
	EmitMessage(context.Context, string, json.RawMessage, ...contract.MessageStatus) (string, error)
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
	target contract.ActorRef,
	content json.RawMessage,
) (contract.Message, error) {
	userKey = strings.TrimSpace(userKey)
	target.Key = strings.TrimSpace(target.Key)
	if userKey == "" || !target.ValidTarget() || validateContent(content) != nil {
		return contract.Message{}, ErrInvalid
	}
	taskID := uuid.V7()
	input := model.Message{
		ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID,
		Kind: contract.ActorKindUser, ActorKey: userKey, Content: content,
		TargetKind: target.Kind, TargetKey: target.Key, DispatchPending: true,
	}
	message, err := service.repo.CreateChatInput(ctx, input)
	if err != nil {
		return contract.Message{}, err
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

// StreamConversation observes a page, not an actor execution or one user input.
func (service *ChatService) StreamConversation(ctx context.Context, convID string, deliver func(delivery.Event) error) error {
	return mapDeliveryError(service.delivery.Stream(ctx, "", convID, "", deliver))
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

func (service *ChatService) EmitMessage(ctx context.Context, messageID string, event json.RawMessage, statuses ...contract.MessageStatus) (string, error) {
	id, err := service.delivery.EmitMessage(ctx, messageID, event, statuses...)
	return id, mapDeliveryError(err)
}
