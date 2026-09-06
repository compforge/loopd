package repo

import (
	"context"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// GetDeliveryInput resolves the message that opened a UI stream, never an answer.
func (store *Store) GetDeliveryInput(ctx context.Context, taskID string) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	rows, err := store.readMessages(ctx, func(tx *gorm.DB) *gorm.DB { return tx.Where("task_id = ? AND purpose = ?", taskID, "input").Limit(1) })
	if err != nil {
		return model.Message{}, err
	}
	if len(rows) == 0 {
		return model.Message{}, ErrNotFound
	}
	return rows[0], nil
}
