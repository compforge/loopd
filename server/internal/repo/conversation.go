package repo

import (
	"context"
	"errors"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConversationRepository interface {
	FindActorConversation(context.Context, string, contract.ActorKind, string) (model.Conversation, error)
	CreateConversation(context.Context, model.Conversation) (model.Conversation, error)
	GetConversation(context.Context, string) (model.Conversation, error)
	ListConversations(context.Context, string, int) ([]model.Conversation, error)
}

func (store *Store) FindActorConversation(ctx context.Context, parentID string, kind contract.ActorKind, key string) (model.Conversation, error) {
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
	query := store.db.WithContext(ctx).Where("parent_id IS NULL AND actor_kind = ?", contract.ActorKindUser)
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

// ParticipantConversation reads the detail association allocated by the message
// transaction. Notification never creates a conversation or hides a missing link.
func (store *Store) ParticipantConversation(ctx context.Context, conversationID string, actor contract.ActorRef) (string, error) {
	if !actor.ValidTarget() {
		return "", nil
	}
	parent, err := store.GetConversation(ctx, conversationID)
	if err != nil {
		return "", err
	}
	if parent.ParentID != nil || parent.ActorKind != contract.ActorKindUser {
		return "", nil
	}
	detail, err := store.FindActorConversation(ctx, conversationID, actor.Kind, actor.Key)
	return detail.ID, err
}

// The caller holds the parent row lock until its message transaction commits.
func ensureParticipantConversation(tx *gorm.DB, parent model.Conversation, actor contract.ActorRef) error {
	if parent.ParentID != nil || parent.ActorKind != contract.ActorKindUser || !actor.ValidTarget() {
		return nil
	}
	var detail model.Conversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("parent_id = ? AND actor_kind = ? AND actor_key = ?", parent.ID, actor.Kind, actor.Key).First(&detail).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	detail = model.Conversation{ID: uuid.V7(), Name: "处理详情", ParentID: &parent.ID, ActorKind: actor.Kind, ActorKey: actor.Key}
	return tx.Create(&detail).Error
}
