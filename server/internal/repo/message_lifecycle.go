package repo

import (
	"context"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
)

// GetMessageStates loads only metadata for a bounded set already being watched.
// An empty conversationID is reserved for internal cross-workspace delivery.
func (store *Store) GetMessageStates(ctx context.Context, conversationID string, ids []string) ([]MessageState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	q := store.db.WithContext(ctx).Select("id", "conversation_id", "purpose", "revision", "status", "human_due_at").Where("id IN ?", ids)
	if conversationID != "" {
		q = q.Where("conversation_id = ?", conversationID)
	}
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
