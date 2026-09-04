package service

import (
	"context"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
)

func (service *Service) CreateConversation(ctx context.Context, name, parentMessageID string) (loopd.Conversation, error) {
	conversation := model.Conversation{ID: uuid.V7(), Name: strings.TrimSpace(name)}
	if parentMessageID != "" {
		parent, err := service.repo.GetMessage(ctx, parentMessageID)
		if err != nil {
			return loopd.Conversation{}, err
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
	return conversationFromModel(conversation), err
}

func (service *Service) GetConversation(ctx context.Context, id string) (loopd.Conversation, error) {
	conversation, err := service.repo.GetConversation(ctx, id)
	return conversationFromModel(conversation), err
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
