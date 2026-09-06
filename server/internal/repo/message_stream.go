package repo

import (
	"context"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

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
