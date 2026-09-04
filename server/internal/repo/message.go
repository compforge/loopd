package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

type MessageRepository interface {
	CreateMessage(context.Context, model.Message) (model.Message, error)
	CreateChatMessages(context.Context, model.Message, model.Message) (model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
	ListMessages(context.Context, string, string, int) ([]model.Message, error)
	UpdateMessageContent(context.Context, string, string, []byte) (model.Message, error)
}

func (store *Store) CreateMessage(ctx context.Context, message model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	if err := ensureConversation(store.db.WithContext(ctx), message.ConversationID); err != nil {
		return model.Message{}, err
	}
	if err := mapError(store.db.WithContext(ctx).Create(&message).Error); err != nil {
		return model.Message{}, err
	}
	return message, nil
}

func (store *Store) CreateChatMessages(
	ctx context.Context,
	userMessage model.Message,
	responderMessage model.Message,
) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if userMessage.ConversationID != responderMessage.ConversationID || userMessage.TaskID != responderMessage.TaskID {
			return ErrConflict
		}
		if err := ensureConversation(tx, userMessage.ConversationID); err != nil {
			return err
		}
		if err := mapError(tx.Create(&userMessage).Error); err != nil {
			return err
		}
		return mapError(tx.Create(&responderMessage).Error)
	})
	if err != nil {
		return model.Message{}, err
	}
	return responderMessage, nil
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
	if result.RowsAffected == 0 {
		return model.Message{}, ErrNotFound
	}
	var message model.Message
	if err := store.db.WithContext(ctx).
		First(&message, "conversation_id = ? AND id = ?", conversationID, id).Error; err != nil {
		return model.Message{}, mapError(err)
	}
	return message, nil
}

func ensureConversation(db *gorm.DB, id string) error {
	var count int64
	if err := db.Model(&model.Conversation{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
