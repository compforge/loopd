package repo

import (
	"context"
	"errors"
	"time"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// EnsureDetailMessage serializes identity creation on the parent response row.
// MySQL uses a row lock; the SQLite Quick Start pool serializes transactions.
//
// +spec=`一个主回答最多对应一个详情 Conversation；同一临时 Harness 的重复交付复用 Message`
// +why=`用父回答行串行创建，避免不同 server 实例为并行输出建立重复身份`
func (store *Store) EnsureDetailMessage(ctx context.Context, conversation model.Conversation, message model.Message) (model.Message, bool, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	if conversation.ParentMessageID == nil {
		return model.Message{}, false, ErrConflict
	}
	// Streaming deltas usually refer to an existing Message. Avoid taking the
	// parent write lock for every token; creation still rechecks under the lock.
	var existing model.Message
	err := store.db.WithContext(ctx).
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.parent_message_id = ? AND messages.task_id = ? AND messages.kind = ? AND messages.actor_key = ?",
			*conversation.ParentMessageID, message.TaskID, message.Kind, message.ActorKey).
		First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Message{}, false, mapError(err)
	}
	created := false
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, "id = ?", *conversation.ParentMessageID).Error; err != nil {
			return err
		}
		var root model.Conversation
		if err := tx.First(&root, "id = ?", parent.ConversationID).Error; err != nil {
			return err
		}
		if parent.Purpose != "" && parent.Purpose != "response" {
			return ErrConflict
		}
		if root.ParentMessageID != nil || parent.Kind != "operator" || parent.TaskID != message.TaskID {
			return ErrConflict
		}
		var detail model.Conversation
		err := tx.Where("parent_message_id = ?", parent.ID).First(&detail).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			detail = conversation
			if err := tx.Create(&detail).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		message.ConversationID = detail.ID
		var existing model.Message
		err = tx.Where("conversation_id = ? AND task_id = ? AND kind = ? AND actor_key = ?",
			detail.ID, message.TaskID, message.Kind, message.ActorKey).First(&existing).Error
		if err == nil {
			message = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return message, created, mapError(err)
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
		if err := ensureConversation(tx, userMessage.ConversationID); err != nil {
			return err
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
		Where("messages.task_id = ? AND conversations.parent_message_id IS NULL", taskID).
		Order("messages.id ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
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

// SaveDetailContent materializes an already observed stream. GORM's automatic
// updated_at would incorrectly extend every Harness to Task completion time.
func (store *Store) SaveDetailContent(ctx context.Context, id string, content []byte) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", id).UpdateColumn("content", content).Error)
}
