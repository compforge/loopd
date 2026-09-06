package repo

import (
	"context"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MessageRepository interface {
	GetMessages(context.Context, string, []string) ([]model.Message, error)
	ListHumanReplies(context.Context, string, []string) ([]model.Message, error)
	Speak(context.Context, string, contract.SpeakRequest) (model.Message, error)
	CreateMessage(context.Context, model.Message) (model.Message, error)
	GetMessage(context.Context, string) (model.Message, error)
	ListMessages(context.Context, string, string, int) ([]model.Message, error)
	ListRootMessagesByTask(context.Context, string) ([]model.Message, error)
	UpdateMessageContent(context.Context, string, string, []byte) (model.Message, error)
}

func (store *Store) CreateMessage(ctx context.Context, message model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation model.Conversation
		if err := tx.First(&conversation, "id = ?", message.ConversationID).Error; err != nil {
			return err
		}
		return store.saveMessage(tx, &message, true)
	})
	return message, mapError(err)
}

func (store *Store) ListRootMessagesByTask(ctx context.Context, taskID string) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		return tx.Joins("JOIN conversations ON conversations.id = messages.conversation_id").Where("messages.task_id = ? AND conversations.parent_id IS NULL", taskID).Order("messages.id ASC")
	})
}

func (store *Store) GetMessage(ctx context.Context, id string) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	rows, err := store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Where("id = ?", id).Limit(1) })
	if err != nil {
		return model.Message{}, err
	}
	if len(rows) == 0 {
		return model.Message{}, ErrNotFound
	}
	return rows[0], nil
}

// MessageState contains routing and progress without a content snapshot.
type MessageState struct {
	ID             string
	ConversationID string
	Purpose        string
	Revision       uint64
	Status         contract.MessageStatus
	Ended          bool
}

// GetMessageState reads progress without loading message bodies.
func (store *Store) GetMessageState(ctx context.Context, id string) (MessageState, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var m model.Message
	err := store.db.WithContext(ctx).Select("id", "conversation_id", "purpose", "revision", "status").First(&m, "id = ?", id).Error
	return MessageState{ID: m.ID, ConversationID: m.ConversationID, Purpose: m.Purpose, Revision: m.Revision, Status: contract.MessageStatus(m.Status), Ended: (contract.Message{Status: contract.MessageStatus(m.Status)}).Ended()}, mapError(err)
}

func (store *Store) ListMessages(ctx context.Context, conversationID, after string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		q := tx.Where("conversation_id = ?", conversationID)
		if after != "" {
			q = q.Where("id > ?", after)
		}
		return q.Order("id ASC").Limit(limit)
	})
}

func (store *Store) UpdateMessageContent(ctx context.Context, conversationID, id string, content []byte) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var m model.Message
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "conversation_id = ? AND id = ?", conversationID, id).Error; err != nil {
			return err
		}
		m.Content = content
		m.Revision++
		return store.saveMessage(tx, &m, false)
	})
	return m, mapError(err)
}

// DeleteMessage removes the message and every physical part in one transaction.
func (store *Store) DeleteMessage(ctx context.Context, id string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Where("message_id = ?", id).Delete(&model.MessagePart{}).Error; err != nil {
			return err
		}
		return tx.Delete(&m).Error
	}))
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

func (store *Store) CreateChatInput(ctx context.Context, input model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	input.Purpose = "input"
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation model.Conversation
		if err := tx.First(&conversation, "id = ?", input.ConversationID).Error; err != nil {
			return err
		}
		if conversation.ParentID != nil || conversation.ActorKind != contract.ActorKindUser {
			return ErrConflict
		}
		return store.saveMessage(tx, &input, true)
	})
	return input, mapError(err)
}

// ListDeliveryMessages observes a user conversation and its direct actor workspaces.
func (store *Store) ListDeliveryMessages(ctx context.Context, conversationID string) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		return tx.Joins("JOIN conversations ON conversations.id = messages.conversation_id").Where("messages.conversation_id = ? OR conversations.parent_id = ?", conversationID, conversationID).Order("messages.id ASC")
	})
}

// GetMessages resolves IDs only within the already selected conversation.
func (store *Store) GetMessages(ctx context.Context, conversationID string, ids []string) ([]model.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("conversation_id = ? AND id IN ?", conversationID, ids)
	})
}

// ListHumanReplies finds typed answers without expanding a reply chain.
func (store *Store) ListHumanReplies(ctx context.Context, conversationID string, questionIDs []string) ([]model.Message, error) {
	if len(questionIDs) == 0 {
		return nil, nil
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		return tx.Where("conversation_id = ? AND reply_to_id IN ? AND purpose = ?", conversationID, questionIDs, "human_reply").Order("id ASC")
	})
}
