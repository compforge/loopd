package repo

import (
	"context"

	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskPair accepts explicit identities, or an unambiguous legacy two-message pair.
// It never picks a last/nearest message when multiple actors speak in parallel.
func TaskPair(rows []model.Message) (input, response model.Message, err error) {
	for _, row := range rows {
		switch row.Purpose {
		case "input":
			if input.ID != "" {
				return input, response, ErrConflict
			}
			input = row
		case "response":
			if response.ID != "" {
				return input, response, ErrConflict
			}
			response = row
		}
	}
	if input.ID == "" && response.ID == "" && len(rows) == 2 && rows[0].Purpose == "" && rows[1].Purpose == "" {
		for _, row := range rows {
			if row.Kind == "user" {
				input = row
			} else {
				response = row
			}
		}
	}
	if input.ID == "" || response.ID == "" || input.Kind != "user" || response.Kind == "user" || input.TaskID != response.TaskID || input.ConversationID != response.ConversationID {
		return input, response, ErrNotFound
	}
	return input, response, nil
}

// withTask uses the initial response as the lock shared by output creation,
// Human transitions and Task completion. No external call runs inside the transaction.
func (store *Store) withTask(ctx context.Context, taskID string, fn func(*gorm.DB, model.Message, model.Message) error) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	rows, err := store.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return err
	}
	input, response, err := TaskPair(rows)
	if err != nil {
		return err
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&response, "id = ?", response.ID).Error; err != nil {
			return err
		}
		if input.Purpose == "" {
			if err := tx.Model(&model.Message{}).Where("id = ?", input.ID).Update("purpose", "input").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Message{}).Where("id = ?", response.ID).Updates(map[string]any{"purpose": "response", "reply_to_message_id": input.ID}).Error; err != nil {
				return err
			}
		}
		return fn(tx, input, response)
	})
	return mapError(err)
}
