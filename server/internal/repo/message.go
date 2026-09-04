package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
)

type MessageRepository interface {
	CreateMessage(context.Context, model.Message) (model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
	ListMessages(context.Context, string, string, int) ([]model.Message, error)
}

func (store *Store) CreateMessage(ctx context.Context, message model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var count int64
	if err := store.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", message.ConversationID).Count(&count).Error; err != nil {
		return model.Message{}, err
	}
	if count == 0 {
		return model.Message{}, ErrNotFound
	}
	if err := mapError(store.db.WithContext(ctx).Create(&message).Error); err != nil {
		return model.Message{}, err
	}
	return message, nil
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
