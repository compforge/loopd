package repo

import (
	"context"
	"github.com/compforge/loopd/server/internal/model"
)

// GetDeliveryInput resolves the message that opened a UI stream, never an answer.
func (store *Store) GetDeliveryInput(ctx context.Context, taskID string) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var input model.Message
	err := store.db.WithContext(ctx).Where("task_id = ? AND purpose = ?", taskID, "input").First(&input).Error
	return input, mapError(err)
}
