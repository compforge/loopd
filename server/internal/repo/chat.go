package repo

import (
	"context"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetDeliveryInput resolves the message that opened a UI stream, never an answer.
func (store *Store) GetDeliveryInput(ctx context.Context, taskID string) (model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var input model.Message
	err := store.db.WithContext(ctx).Where("task_id = ? AND purpose = ?", taskID, "input").First(&input).Error
	return input, mapError(err)
}

// withDelivery serializes only the UI closing intent, not Operator execution.
func (store *Store) withDelivery(ctx context.Context, taskID string, fn func(*gorm.DB, model.Message) error) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var input model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id = ? AND purpose = ?", taskID, "input").First(&input).Error; err != nil {
			return err
		}
		return fn(tx, input)
	}))
}
