package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
)

type ConversationRepository interface {
	FindActorConversation(context.Context, string, string, string) (model.Conversation, error)
	CreateConversation(context.Context, model.Conversation) (model.Conversation, error)
	GetConversation(context.Context, string) (model.Conversation, error)
	ListConversations(context.Context, string, int) ([]model.Conversation, error)
}

func (store *Store) FindActorConversation(ctx context.Context, parentID, kind, key string) (model.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var value model.Conversation
	err := store.db.WithContext(ctx).Where("parent_id = ? AND actor_kind = ? AND actor_key = ?", parentID, kind, key).Order("id ASC").First(&value).Error
	return value, mapError(err)
}

// ListConversations returns root conversations newest first. Detail
// conversations belong to a parent and actor and are not top-level chat
// navigation entries.
func (store *Store) ListConversations(ctx context.Context, before string, limit int) ([]model.Conversation, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	query := store.db.WithContext(ctx).Where("parent_id IS NULL AND actor_kind = ?", "user")
	if before != "" {
		query = query.Where("id < ?", before)
	}
	var conversations []model.Conversation
	if err := query.Order("id DESC").Limit(limit).Find(&conversations).Error; err != nil {
		return nil, mapError(err)
	}
	return conversations, nil
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
