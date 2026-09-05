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
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
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
	taskID string,
) (loopd.Conversation, error) {
	conversation := model.Conversation{
		ID: uuid.V7(), Name: strings.TrimSpace(name),
		ActorKind: string(loopd.RoleUser), ActorKey: strings.TrimSpace(userKey),
	}
	if taskID != "" {
		rows, err := service.repo.ListRootMessagesByTask(ctx, taskID)
		if err != nil {
			return loopd.Conversation{}, err
		}
		input, _, err := repo.ChatMessages(rows)
		if err != nil {
			return loopd.Conversation{}, err
		}
		// Ownership follows the logical task target, never a worker instance
		// or the sender of whichever message happens to arrive last.
		conversation.ActorKind, conversation.ActorKey = input.TargetKind, input.TargetKey
		conversation.TaskID = &taskID
	} else if conversation.ActorKey == "" {
		return loopd.Conversation{}, ErrInvalid
	}
	conversation, err := service.repo.CreateConversation(ctx, conversation)
	if err == nil {
		service.logger.InfoContext(ctx, "conversation created",
			"conversation_id", conversation.ID,
			"task_id", taskID, "actor_kind", conversation.ActorKind,
		)
	}
	return conversationFromModel(conversation), err
}

func (service *ConversationService) GetConversation(ctx context.Context, id string) (loopd.Conversation, error) {
	conversation, err := service.repo.GetConversation(ctx, id)
	return conversationFromModel(conversation), err
}

func (service *ConversationService) ListDetailConversations(ctx context.Context, taskID string) ([]loopd.Conversation, error) {
	conversation, err := service.repo.FindConversationByTask(ctx, taskID)
	if errors.Is(err, repo.ErrNotFound) {
		return []loopd.Conversation{}, nil
	}
	if err != nil {
		return nil, err
	}
	return []loopd.Conversation{conversationFromModel(conversation)}, nil
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
		ActorKind: loopd.Role(value.ActorKind), ActorKey: value.ActorKey,
		Timestamped: loopd.Timestamped{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt},
	}
	if value.TaskID != nil {
		result.TaskID = *value.TaskID
	}
	return result
}
