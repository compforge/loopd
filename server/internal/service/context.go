package service

import (
	"context"
	"log/slog"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type ContextRepository interface {
	GetMessage(context.Context, string) (model.Message, error)
	GetConversation(context.Context, string) (model.Conversation, error)
	ListMessagesThrough(context.Context, string, string, int) ([]model.Message, bool, error)
}

func (service *ContextService) GetMessageContext(ctx context.Context, convID, messageID string) (loopd.MessageContext, error) {
	message, err := service.repo.GetMessage(ctx, messageID)
	if err != nil {
		return loopd.MessageContext{}, err
	}
	if message.ConversationID != convID {
		return loopd.MessageContext{}, repo.ErrNotFound
	}
	conv, err := service.repo.GetConversation(ctx, convID)
	if err != nil {
		return loopd.MessageContext{}, err
	}
	rows, earlier, err := service.repo.ListMessagesThrough(ctx, convID, messageID, defaultPageSize)
	if err != nil {
		return loopd.MessageContext{}, err
	}
	result := loopd.MessageContext{Conversation: conversationFromModel(conv), Message: messageFromModel(message), HasEarlier: earlier, History: make([]loopd.Message, 0, len(rows))}
	for _, row := range rows {
		result.History = append(result.History, messageFromModel(row))
	}
	if len(result.History) > 0 {
		result.HistoryFromMessageID = result.History[0].ID
	}
	return result, nil
}

// ContextService resolves a message and its bounded conversation history.
type ContextService struct {
	repo   ContextRepository
	logger *slog.Logger
}

func NewContextService(repository ContextRepository, logger *slog.Logger) *ContextService {
	return &ContextService{repo: repository, logger: loggerOrDefault(logger)}
}
