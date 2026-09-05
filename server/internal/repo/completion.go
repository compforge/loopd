package repo

import (
	"context"
	"errors"
	"time"

	"github.com/compforge/loopd/server/internal/domain"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

// BeginCompletion closes the create/reply gate durably before external delivery.
// completion is persisted so Run can retry after process death or delivery failure.
func (store *Store) BeginCompletion(ctx context.Context, taskID string, completion []byte, failed bool) error {
	blocked := false
	err := store.withChat(ctx, taskID, func(tx *gorm.DB, input, response model.Message) error {
		if input.DeliveryState != "" {
			if string(input.Completion) != string(completion) {
				return ErrConflict
			}
			return nil
		}
		var pending []model.Message
		if err := tx.Where("task_id = ? AND purpose = ? AND human_due_at IS NOT NULL", taskID, "human_request").Find(&pending).Error; err != nil {
			return err
		}
		for i := range pending {
			m := &pending[i]
			c, err := decodeHuman(*m)
			if err != nil {
				return err
			}
			if err := expireHuman(tx, m, &c, time.Now().UTC(), true); err != nil {
				return err
			}
			question := humanQuestion(*m, c)
			changed, err := question.EndTask(failed)
			if errors.Is(err, domain.ErrHumanConflict) {
				blocked = true
				continue
			}
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			c.Blocks[0].Status, c.Blocks[0].Reason = question.Status, question.Reason
			if err := saveHuman(tx, m, c, false); err != nil {
				return err
			}
		}
		if blocked {
			return nil
		}
		if err := tx.Model(&model.Message{}).Where("task_id = ? AND wake_pending = ?", taskID, true).Update("wake_pending", false).Error; err != nil {
			return err
		}
		return tx.Model(&input).Updates(map[string]any{"delivery_state": "closing", "completion": completion}).Error
	})
	if err == nil && blocked {
		return ErrConflict
	}
	return err
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
