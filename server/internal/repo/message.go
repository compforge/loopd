package repo

import (
	"context"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MessageRepository interface {
	ProjectOutput(context.Context, string, ui.Event, ...contract.MessageStatus) error
	GetMessageState(context.Context, string) (MessageState, error)
	ExpireMessages(context.Context, time.Time, int) ([]string, error)
	GetMessageStates(context.Context, string, []string) ([]MessageState, error)
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conversation, "id = ?", message.ConversationID).Error; err != nil {
			return err
		}
		if err := ensureParticipantConversation(tx, conversation, contract.ActorRef{Kind: message.TargetKind, Key: message.TargetKey}); err != nil {
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
	HumanDueAt     *time.Time
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

func (store *Store) CreateChatInput(ctx context.Context, input model.Message) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	input.Purpose = "input"
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conversation model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conversation, "id = ?", input.ConversationID).Error; err != nil {
			return err
		}
		if conversation.ParentID != nil || conversation.ActorKind != contract.ActorKindUser {
			return ErrConflict
		}
		if err := ensureParticipantConversation(tx, conversation, contract.ActorRef{Kind: input.TargetKind, Key: input.TargetKey}); err != nil {
			return err
		}
		return store.saveMessage(tx, &input, true)
	})
	return input, mapError(err)
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

// GetMessageStates loads only metadata for a bounded set already being watched.
func (store *Store) GetMessageStates(ctx context.Context, conversationID string, ids []string) ([]MessageState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	q := store.db.WithContext(ctx).Select("id", "conversation_id", "purpose", "revision", "status", "human_due_at").Where("conversation_id = ? AND id IN ?", conversationID, ids)
	if err := q.Find(&rows).Error; err != nil {
		return nil, mapError(err)
	}
	states := make([]MessageState, 0, len(rows))
	for _, row := range rows {
		status := contract.MessageStatus(row.Status)
		states = append(states, MessageState{ID: row.ID, ConversationID: row.ConversationID, Purpose: row.Purpose, Revision: row.Revision, Status: status, Ended: status.Terminal(), HumanDueAt: row.HumanDueAt})
	}
	return states, nil
}

// ExpireMessages closes inactive output without reading or rewriting its body.
// +spec=`Only streaming messages with updated_at <= now-TTL expire. A concurrent accepted write or End wins over stale cleanup candidates.`
func (store *Store) ExpireMessages(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	err := store.db.WithContext(ctx).Select("id", "revision", "target_kind").
		Where("status = ? AND updated_at <= ?", contract.MessageStatusStreaming, cutoff).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, mapError(err)
	}
	var expired []string
	for _, row := range rows {
		// Leave updated_at at the last actual output time: expiry is not activity.
		result := store.db.WithContext(ctx).Model(&model.Message{}).
			Where("id = ? AND status = ? AND revision = ? AND updated_at <= ?", row.ID, contract.MessageStatusStreaming, row.Revision, cutoff).
			UpdateColumns(map[string]any{"status": contract.MessageStatusExpired, "revision": row.Revision + 1, "dispatch_pending": row.TargetKind != contract.ActorKindUser})
		if result.Error != nil {
			return expired, mapError(result.Error)
		}
		if result.RowsAffected == 1 {
			expired = append(expired, row.ID)
		}
	}
	return expired, nil
}

// LatestMessageID reads the discovery boundary without loading message bodies.
func (store *Store) LatestMessageID(ctx context.Context, convID string) (string, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var ids []string
	err := store.db.WithContext(ctx).Model(&model.Message{}).Where("conversation_id = ?", convID).Order("id DESC").Limit(1).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

// ListStreamMessages never walks ended history or implicitly includes child convs.
// UUIDv7 follows the existing message allocation-order assumption, not a DB commit log.
func (store *Store) ListStreamMessages(ctx context.Context, convID, after, watermark string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		q := tx.Where("conversation_id = ? AND id > ?", convID, after)
		if after < watermark {
			q = q.Where("id > ? OR status = ? OR human_due_at IS NOT NULL", watermark, contract.MessageStatusStreaming)
		}
		return q.Order("id ASC").Limit(limit)
	})
}
