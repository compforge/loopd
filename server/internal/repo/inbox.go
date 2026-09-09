package repo

import (
	"context"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// ListInbox queries SQL, not the CRD wake signal, for the next addressed batch.
// All statuses are readable. The Operator decides how to use a streaming snapshot
// and when it is safe to Commit; ID-based discovery does not replay later revisions.
func (store *Store) ListInbox(ctx context.Context, conversationID string, kind contract.ActorKind, key, after string, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	messages, err := store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB {
		return tx.
			Where("conversation_id = ? AND id > ?", conversationID, after).
			Where("(target_kind = ? AND target_key = ?) OR (target_kind = ? AND target_key = ?)", kind, key, "", "").
			Where("NOT (kind = ? AND actor_key = ?)", kind, key).
			Order("id ASC").Limit(limit)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return messages, nil
}

func (store *Store) PendingDispatches(ctx context.Context, limit int) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	messages, err := store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Where("dispatch_pending = ?", true).Order("id ASC").Limit(limit) })
	return messages, mapError(err)
}

func (store *Store) AcknowledgeDispatch(ctx context.Context, messageID string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", messageID).UpdateColumn("dispatch_pending", false).Error)
}
