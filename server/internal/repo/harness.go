package repo

import (
	"context"
	"time"

	"github.com/compforge/loopd/server/internal/model"
)

func (store *Store) RegisterHarness(ctx context.Context, harness model.Harness) (model.Harness, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var existing model.Harness
	result := store.db.WithContext(ctx).Where("key = ?", harness.Key).Limit(1).Find(&existing)
	if result.Error != nil {
		return model.Harness{}, mapError(result.Error)
	}
	if result.RowsAffected > 0 {
		existing.DisplayName = harness.DisplayName
		existing.Description = harness.Description
		existing.ExpiresAt = harness.ExpiresAt
		if err := store.db.WithContext(ctx).Save(&existing).Error; err != nil {
			return model.Harness{}, mapError(err)
		}
		return existing, nil
	}
	if err := store.db.WithContext(ctx).Create(&harness).Error; err != nil {
		return model.Harness{}, mapError(err)
	}
	return harness, nil
}

func (store *Store) ListHarnesses(ctx context.Context, aliveAfter time.Time) ([]model.Harness, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()

	var harnesses []model.Harness
	if err := store.db.WithContext(ctx).
		Where("expires_at > ?", aliveAfter).
		Order("key ASC").
		Find(&harnesses).Error; err != nil {
		return nil, mapError(err)
	}
	return harnesses, nil
}
