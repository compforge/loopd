package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

type ChatRepository interface {
	CreateChatInput(context.Context, model.Message) (model.Message, error)
}

// ChatService accepts user input independently of Conv page listeners.
// Answers are created only when actors publish.
type ChatService struct {
	notifier MessageNotifier
	repo     ChatRepository
	logger   *slog.Logger
}

type MessageNotifier interface {
	Notify(context.Context, model.Message) error
}

func NewChatService(repository ChatRepository, logger *slog.Logger, notifier MessageNotifier) *ChatService {
	return &ChatService{repo: repository, logger: loggerOrDefault(logger), notifier: notifier}
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
		SourceKind: contract.ActorKindUser, SourceKey: userKey, Content: content,
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
