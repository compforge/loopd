package repo

import (
	"context"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

type MessageRepository interface {
	CreateMessage(context.Context, model.Message) (model.Message, error)
	CreateChatMessages(context.Context, model.Message, model.Message, func(context.Context) error) (model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
	ListMessages(context.Context, string, string, int) ([]model.Message, error)
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	ListMessagesThrough(context.Context, string, string, int) ([]model.Message, bool, error)
	UpdateMessageContent(context.Context, string, string, []byte) (model.Message, error)
}

func (store *Store) CreateMessage(ctx context.Context, message model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var conversation model.Conversation
	if err := store.db.WithContext(ctx).First(&conversation, "id = ?", message.ConversationID).Error; err != nil {
		return model.Message{}, mapError(err)
	}
	if conversation.TaskID != nil && *conversation.TaskID != message.TaskID {
		return model.Message{}, ErrConflict
	}
	if err := mapError(store.db.WithContext(ctx).Create(&message).Error); err != nil {
		return model.Message{}, err
	}
	return message, nil
}

func (store *Store) CreateChatMessages(
	ctx context.Context,
	userMessage model.Message,
	responseMessage model.Message,
	beforeCommit func(context.Context) error,
) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	userMessage.Purpose = "input"
	responseMessage.Purpose = "response"
	responseMessage.ReplyToMessageID = userMessage.ID
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if userMessage.ConversationID != responseMessage.ConversationID || userMessage.TaskID != responseMessage.TaskID {
			return ErrConflict
		}
		var conversation model.Conversation
		if err := tx.First(&conversation, "id = ?", userMessage.ConversationID).Error; err != nil {
			return mapError(err)
		}
		if conversation.TaskID != nil || conversation.ActorKind != "user" {
			return ErrConflict
		}
		if err := mapError(tx.Create(&userMessage).Error); err != nil {
			return err
		}
		if err := mapError(tx.Create(&responseMessage).Error); err != nil {
			return err
		}
		if beforeCommit != nil {
			return beforeCommit(ctx)
		}
		return nil
	})
	if err != nil {
		return model.Message{}, err
	}
	return responseMessage, nil
}

func (store *Store) ListRootMessagesByTask(ctx context.Context, taskID string) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var messages []model.Message
	if err := store.db.WithContext(ctx).
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("messages.task_id = ? AND conversations.task_id IS NULL", taskID).
		Order("messages.id ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

// ListMessagesByTask includes both user-conversation and work-conversation
// messages. It does not infer the current input from the last user reply.
func (store *Store) ListMessagesByTask(ctx context.Context, taskID, after string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	query := store.db.WithContext(ctx).Where("task_id = ?", taskID)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	var messages []model.Message
	err := query.Order("id ASC").Limit(limit).Find(&messages).Error
	return messages, mapError(err)
}

func (store *Store) ListMessagesThrough(
	ctx context.Context,
	conversationID string,
	throughID string,
	limit int,
) ([]model.Message, bool, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var messages []model.Message
	if err := store.db.WithContext(ctx).
		Where("conversation_id = ? AND id <= ?", conversationID, throughID).
		Order("id DESC").
		Limit(limit + 1).
		Find(&messages).Error; err != nil {
		return nil, false, err
	}
	hasEarlier := len(messages) > limit
	if hasEarlier {
		messages = messages[:limit]
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	return messages, hasEarlier, nil
}

func (store *Store) GetMessage(ctx context.Context, id string) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var message model.Message
	if err := store.db.WithContext(ctx).First(&message, "id = ?", id).Error; err != nil {
		return model.Message{}, mapError(err)
	}
	return message, nil
}

func (store *Store) ListMessages(ctx context.Context, conversationID, after string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	query := store.db.WithContext(ctx).Where("conversation_id = ?", conversationID)
	if after != "" {
		query = query.Where("id > ?", after)
	}
	var messages []model.Message
	if err := query.Order("id ASC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

func (store *Store) UpdateMessageContent(
	ctx context.Context,
	conversationID string,
	id string,
	content []byte,
) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	result := store.db.WithContext(ctx).Model(&model.Message{}).
		Where("conversation_id = ? AND id = ?", conversationID, id).
		Update("content", content)
	if result.Error != nil {
		return model.Message{}, mapError(result.Error)
	}
	// Repeated completion may write an identical snapshot. MySQL can report
	// zero changed rows for an existing Message; the lookup decides existence.
	var message model.Message
	if err := store.db.WithContext(ctx).
		First(&message, "conversation_id = ? AND id = ?", conversationID, id).Error; err != nil {
		return model.Message{}, mapError(err)
	}
	return message, nil
}

// ObserveMessageActivity only widens the interval. Conditional updates remain
// safe when different servers deliver accepted events to SQL out of order.
func (store *Store) ObserveMessageActivity(ctx context.Context, id string, at time.Time) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	if err := store.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ? AND created_at > ?", id, at).UpdateColumn("created_at", at).Error; err != nil {
		return mapError(err)
	}
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ? AND updated_at < ?", id, at).UpdateColumn("updated_at", at).Error)
}
