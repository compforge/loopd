package repo

import (
	"context"
	"time"

	"github.com/compforge/loopd/server/internal/model"
)

func (store *Store) RegisterOperator(ctx context.Context, operator model.Operator) (model.Operator, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var existing model.Operator
	result := store.db.WithContext(ctx).Where("key = ?", operator.Key).Limit(1).Find(&existing)
	if result.Error != nil {
		return model.Operator{}, mapError(result.Error)
	}
	if result.RowsAffected > 0 {
		existing.DisplayName = operator.DisplayName
		existing.Description = operator.Description
		existing.ExpiresAt = operator.ExpiresAt
		if err := store.db.WithContext(ctx).Save(&existing).Error; err != nil {
			return model.Operator{}, mapError(err)
		}
		return existing, nil
	}
	if err := store.db.WithContext(ctx).Create(&operator).Error; err != nil {
		return model.Operator{}, mapError(err)
	}
	return operator, nil
}

func (store *Store) ListOperators(ctx context.Context, aliveAfter time.Time) ([]model.Operator, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var operators []model.Operator
	if err := store.db.WithContext(ctx).
		Where("expires_at > ?", aliveAfter).
		Order("key ASC").
		Find(&operators).Error; err != nil {
		return nil, mapError(err)
	}
	return operators, nil
}
