package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

func (service *ConversationService) FindActorConversation(ctx context.Context, parentID string, kind contract.ActorKind, key string) ([]contract.Conversation, error) {
	value, err := service.repo.FindActorConversation(ctx, parentID, kind, key)
	if errors.Is(err, repo.ErrNotFound) {
		return []contract.Conversation{}, nil
	}
	if err != nil {
		return nil, err
	}
	return []contract.Conversation{conversationFromModel(value)}, nil
}

type ConversationService struct {
	repo   repo.ConversationRepository
	logger *slog.Logger
}

func NewConversationService(repository repo.ConversationRepository, logger *slog.Logger) *ConversationService {
	return &ConversationService{repo: repository, logger: loggerOrDefault(logger)}
}

func (service *ConversationService) CreateConversation(
	ctx context.Context,
	name string,
	userKey string,
) (contract.Conversation, error) {
	conversation := model.Conversation{
		ID: uuid.V7(), Name: strings.TrimSpace(name),
		ActorKind: contract.ActorKindUser, ActorKey: strings.TrimSpace(userKey),
	}
	if conversation.ActorKey == "" {
		return contract.Conversation{}, ErrInvalid
	}
	conversation, err := service.repo.CreateConversation(ctx, conversation)
	if err == nil {
		service.logger.InfoContext(ctx, "conversation created",
			"conversation_id", conversation.ID,
			"actor_kind", conversation.ActorKind,
		)
	}
	return conversationFromModel(conversation), err
}

func (service *ConversationService) GetConversation(ctx context.Context, id string) (contract.Conversation, error) {
	conversation, err := service.repo.GetConversation(ctx, id)
	return conversationFromModel(conversation), err
}

func (service *ConversationService) ListConversations(
	ctx context.Context,
	before string,
	limit int,
) ([]contract.Conversation, error) {
	conversations, err := service.repo.ListConversations(ctx, before, pageSize(limit))
	if err != nil {
		return nil, err
	}
	result := make([]contract.Conversation, len(conversations))
	for index := range conversations {
		result[index] = conversationFromModel(conversations[index])
	}
	return result, nil
}

func conversationFromModel(value model.Conversation) contract.Conversation {
	result := contract.Conversation{
		ID: value.ID, Name: value.Name,
		ActorKind: value.ActorKind, ActorKey: value.ActorKey,
		Timestamped: contract.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
	}
	if value.ParentID != nil {
		result.ParentID = *value.ParentID
	}
	return result
}
