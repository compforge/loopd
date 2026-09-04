package service

import (
	"context"
	"log/slog"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type TaskRepository interface {
	GetConversation(context.Context, string) (model.Conversation, error)
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	ListMessagesThrough(context.Context, string, string, int) ([]model.Message, bool, error)
}

// TaskService resolves Task context without introducing a tasks table.
type TaskService struct {
	repo   TaskRepository
	logger *slog.Logger
}

func NewTaskService(repository TaskRepository, logger *slog.Logger) *TaskService {
	return &TaskService{repo: repository, logger: loggerOrDefault(logger)}
}

// GetContext reconstructs the current task input, response, and bounded
// conversation history from Messages. Task lifecycle is not duplicated in a
// database table.
func (service *TaskService) GetContext(ctx context.Context, taskID string) (loopd.TaskContext, error) {
	rows, err := service.repo.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return loopd.TaskContext{}, err
	}
	var input, response model.Message
	for _, row := range rows {
		if row.Kind == string(loopd.RoleUser) {
			input = row
		} else {
			response = row
		}
	}
	if input.ID == "" || response.ID == "" || input.ConversationID != response.ConversationID {
		return loopd.TaskContext{}, repo.ErrNotFound
	}
	conversation, err := service.repo.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return loopd.TaskContext{}, err
	}
	historyRows, hasEarlier, err := service.repo.ListMessagesThrough(
		ctx, input.ConversationID, input.ID, defaultPageSize,
	)
	if err != nil {
		return loopd.TaskContext{}, err
	}
	history := make([]loopd.Message, 0, len(historyRows))
	for _, row := range historyRows {
		history = append(history, messageFromModel(row))
	}
	result := loopd.TaskContext{
		ID: taskID, Conversation: conversationFromModel(conversation),
		Input: messageFromModel(input), Response: messageFromModel(response),
		History: history, HasEarlier: hasEarlier,
	}
	if len(history) > 0 {
		result.HistoryFromMessageID = history[0].ID
	}
	service.logger.DebugContext(ctx, "task context resolved",
		"task_id", taskID,
		"conversation_id", input.ConversationID,
		"input_message_id", input.ID,
		"response_message_id", response.ID,
		"history_count", len(history),
		"has_earlier", hasEarlier,
	)
	return result, nil
}
