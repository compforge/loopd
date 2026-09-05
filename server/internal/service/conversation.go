package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/qiankunli/go-stdx/uuid"
)

type ConversationRepository interface {
	repo.ConversationRepository
	EnsureActorConversation(context.Context, string, loopd.ActorRef) (model.Conversation, error)
}

func (service *ConversationService) ActorConversation(ctx context.Context, parentID string, actor loopd.ActorRef) (loopd.Conversation, error) {
	if !actor.ValidTarget() {
		return loopd.Conversation{}, ErrInvalid
	}
	value, err := service.repo.EnsureActorConversation(ctx, parentID, actor)
	return conversationFromModel(value), err
}

func (service *ConversationService) FindActorConversation(ctx context.Context, parentID, kind, key string) ([]loopd.Conversation, error) {
	value, err := service.repo.FindActorConversation(ctx, parentID, kind, key)
	if errors.Is(err, repo.ErrNotFound) {
		return []loopd.Conversation{}, nil
	}
	if err != nil {
		return nil, err
	}
	return []loopd.Conversation{conversationFromModel(value)}, nil
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
	userKey string,
) (loopd.Conversation, error) {
	conversation := model.Conversation{
		ID: uuid.V7(), Name: strings.TrimSpace(name),
		ActorKind: string(loopd.ActorKindUser), ActorKey: strings.TrimSpace(userKey),
	}
	if conversation.ActorKey == "" {
		return loopd.Conversation{}, ErrInvalid
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
		ActorKind: loopd.ActorKind(value.ActorKind), ActorKey: value.ActorKey,
		Timestamped: loopd.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
	}
	if value.ParentID != nil {
		result.ParentID = *value.ParentID
	}
	return result
}
