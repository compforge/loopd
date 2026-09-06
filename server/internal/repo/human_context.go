package repo

import (
	"context"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Question identity is serialized on its conversation; replies/timeouts on the
// parent then question row, matching message allocation lock order. Neither
// lock depends on an open user Chat.
func (s *Store) withHumanContext(ctx context.Context, r contract.HumanRequest, fn func(*gorm.DB) error) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return mapError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", r.ConversationID).Error; err != nil {
			return err
		}
		if r.ReplyToID != "" {
			var ref model.Message
			if err := tx.First(&ref, "id = ? AND conversation_id = ?", r.ReplyToID, r.ConversationID).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	}))
}

func (s *Store) withHumanMessage(ctx context.Context, id string, fn func(*gorm.DB, model.Message) error) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return mapError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var message model.Message
		if err := tx.Select("conversation_id").First(&message, "id = ?", id).Error; err != nil {
			return err
		}
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", message.ConversationID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&message, "id = ?", id).Error; err != nil {
			return err
		}
		if err := hydrateMessage(tx.Clauses(clause.Locking{Strength: "UPDATE"}), &message); err != nil {
			return err
		}
		return fn(tx, message)
	}))
}
