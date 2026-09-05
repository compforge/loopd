package repo

import (
	"context"
	"github.com/compforge/loopd/server/internal/model"
)

// ListInbox queries SQL, not the CRD wake signal, for the next addressed batch.
func (store *Store) ListInbox(ctx context.Context, conversationID, kind, key, after string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var messages []model.Message
	err := store.db.WithContext(ctx).
		Where("conversation_id = ? AND id > ?", conversationID, after).
		Where("(target_kind = ? AND target_key = ?) OR (target_kind = ? AND target_key = ?)", kind, key, "", "").
		Where("NOT (kind = ? AND actor_key = ?)", kind, key).
		Order("id ASC").Limit(limit).Find(&messages).Error
	return messages, mapError(err)
}

func (store *Store) PendingDispatches(ctx context.Context, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var messages []model.Message
	err := store.db.WithContext(ctx).Where("dispatch_pending = ?", true).Order("id ASC").Limit(limit).Find(&messages).Error
	return messages, mapError(err)
}

func (store *Store) AcknowledgeDispatch(ctx context.Context, messageID string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", messageID).UpdateColumn("dispatch_pending", false).Error)
}
