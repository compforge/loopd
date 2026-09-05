package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// BeginCompletion saves UI closure intent before external delivery.
// completion is persisted so Run can retry after process death or delivery failure.
func (store *Store) BeginCompletion(ctx context.Context, taskID string, completion []byte) error {
	return store.withDelivery(ctx, taskID, func(tx *gorm.DB, input model.Message) error {
		if input.DeliveryState != "" {
			if string(input.Completion) != string(completion) {
				return ErrConflict
			}
			return nil
		}
		// Transport completion does not resolve or cancel independent Human questions.
		return tx.Model(&input).Updates(map[string]any{"delivery_state": "closing", "completion": completion}).Error
	})
}

func (store *Store) FinishCompletion(ctx context.Context, taskID string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.db.WithContext(ctx).Model(&model.Message{}).Where("task_id = ? AND purpose = ?", taskID, "input").Update("delivery_state", "closed").Error
}

func (store *Store) PendingCompletions(ctx context.Context) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	err := store.db.WithContext(ctx).Where("purpose = ? AND delivery_state = ?", "input", "closing").Order("id ASC").Find(&rows).Error
	return rows, mapError(err)
}
