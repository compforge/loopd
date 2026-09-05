package service

import (
	"context"
	"log/slog"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type ChatContextRepository interface {
	GetConversation(context.Context, string) (model.Conversation, error)
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	ListMessagesByTask(context.Context, string, string, int) ([]model.Message, error)
	ListMessagesThrough(context.Context, string, string, int) ([]model.Message, bool, error)
}

// ChatContextService resolves the messages and history of one Chat delivery.
type ChatContextService struct {
	repo   ChatContextRepository
	logger *slog.Logger
}

func NewChatContextService(repository ChatContextRepository, logger *slog.Logger) *ChatContextService {
	return &ChatContextService{repo: repository, logger: loggerOrDefault(logger)}
}

func (service *ChatContextService) ListMessages(ctx context.Context, taskID, after string, limit int) ([]loopd.Message, error) {
	rows, err := service.repo.ListMessagesByTask(ctx, taskID, after, pageSize(limit))
	if err != nil {
		return nil, err
	}
	result := make([]loopd.Message, len(rows))
	for i, row := range rows {
		result[i] = messageFromModel(row)
	}
	return result, nil
}

// GetContext reconstructs the current task input, response, and bounded
// conversation history from Messages. Task lifecycle is not duplicated in a
// database table.
func (service *ChatContextService) GetContext(ctx context.Context, taskID string) (loopd.ChatContext, error) {
	rows, err := service.repo.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return loopd.ChatContext{}, err
	}
	input, response, err := repo.ChatMessages(rows)
	if err != nil {
		return loopd.ChatContext{}, err
	}
	conversation, err := service.repo.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return loopd.ChatContext{}, err
	}
	historyRows, hasEarlier, err := service.repo.ListMessagesThrough(
		ctx, input.ConversationID, input.ID, defaultPageSize,
	)
	if err != nil {
		return loopd.ChatContext{}, err
	}
	history := make([]loopd.Message, 0, len(historyRows))
	for _, row := range historyRows {
		history = append(history, messageFromModel(row))
	}
	result := loopd.ChatContext{
		Target:        loopd.ActorRef{Kind: loopd.Role(input.TargetKind), Key: input.TargetKey},
		DeliveryState: input.DeliveryState,
		ID:            taskID, Conversation: conversationFromModel(conversation),
		Input: messageFromModel(input), Response: messageFromModel(response),
		History: history, HasEarlier: hasEarlier,
	}
	if len(history) > 0 {
		result.HistoryFromMessageID = history[0].ID
	}
	service.logger.DebugContext(ctx, "chat context resolved",
		"task_id", taskID,
		"conversation_id", input.ConversationID,
		"input_message_id", input.ID,
		"response_message_id", response.ID,
		"history_count", len(history),
		"has_earlier", hasEarlier,
	)
	return result, nil
}
