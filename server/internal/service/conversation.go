package service

import (
	"context"
	"log/slog"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

type ConversationRepository interface {
	repo.ConversationRepository
	GetMessage(context.Context, string) (model.Message, error)
}

type ConversationService struct {
	repo   ConversationRepository
	logger *slog.Logger
}

func NewConversationService(repository ConversationRepository, logger *slog.Logger) *ConversationService {
	return &ConversationService{repo: repository, logger: loggerOrDefault(logger)}
}

func (service *ConversationService) CreateConversation(
	ctx context.Context,
	name string,
	parentMessageID string,
) (loopd.Conversation, error) {
	conversation := model.Conversation{ID: uuid.V7(), Name: strings.TrimSpace(name)}
	if parentMessageID != "" {
		parent, err := service.repo.GetMessage(ctx, parentMessageID)
		if err != nil {
			return loopd.Conversation{}, err
		}
		if parent.Kind != string(loopd.RoleOperator) && parent.Kind != string(loopd.RoleHarness) {
			return loopd.Conversation{}, ErrInvalid
		}
		parentConversation, err := service.repo.GetConversation(ctx, parent.ConversationID)
		if err != nil {
			return loopd.Conversation{}, err
		}
		if parentConversation.ParentMessageID != nil {
			return loopd.Conversation{}, ErrInvalid
		}
		conversation.ParentMessageID = &parentMessageID
	}
	conversation, err := service.repo.CreateConversation(ctx, conversation)
	if err == nil {
		service.logger.InfoContext(ctx, "conversation created",
			"conversation_id", conversation.ID,
			"parent_message_id", parentMessageID,
		)
	}
	return conversationFromModel(conversation), err
}

func (service *ConversationService) GetConversation(ctx context.Context, id string) (loopd.Conversation, error) {
	conversation, err := service.repo.GetConversation(ctx, id)
	return conversationFromModel(conversation), err
}

func (service *ConversationService) ListConversations(
	ctx context.Context,
	before string,
	limit int,
) ([]loopd.Conversation, error) {
	conversations, err := service.repo.ListConversations(ctx, before, pageSize(limit))
	if err != nil {
		return nil, err
	}
	result := make([]loopd.Conversation, len(conversations))
	for index := range conversations {
		result[index] = conversationFromModel(conversations[index])
	}
	return result, nil
}

func conversationFromModel(value model.Conversation) loopd.Conversation {
	result := loopd.Conversation{
		ID: value.ID, Name: value.Name,
		Timestamped: loopd.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
	}
	if value.ParentMessageID != nil {
		result.ParentMessageID = *value.ParentMessageID
	}
	return result
}
