package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
)

type ConversationRepository interface {
	CreateConversation(context.Context, model.Conversation) (model.Conversation, error)
	GetConversation(context.Context, string) (model.Conversation, error)
}

func (store *Store) CreateConversation(ctx context.Context, conversation model.Conversation) (model.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	if err := mapError(store.db.WithContext(ctx).Create(&conversation).Error); err != nil {
		return model.Conversation{}, err
	}
	return conversation, nil
}

func (store *Store) GetConversation(ctx context.Context, id string) (model.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var conversation model.Conversation
	if err := store.db.WithContext(ctx).First(&conversation, "id = ?", id).Error; err != nil {
		return model.Conversation{}, mapError(err)
	}
	return conversation, nil
}
