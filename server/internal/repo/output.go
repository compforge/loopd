package repo

import (
	"context"
	"encoding/json"
	"errors"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
)

// +spec=`输出按 task/key 复用 Message；actor 不可变，同一 actor 可以有多条输出`
func (store *Store) EnsureOutput(ctx context.Context, taskID string, request loopd.OutputRequest) (message model.Message, err error) {
	err = store.withTask(ctx, taskID, func(tx *gorm.DB, input, response model.Message) error {
		err := tx.Where("task_id = ? AND output_key = ?", taskID, request.Key).First(&message).Error
		if err == nil {
			if message.Kind != string(request.Actor.Kind) || message.ActorKey != request.Actor.Key {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if response.DeliveryState != "" {
			return ErrConflict
		}
		var conversation model.Conversation
		err = tx.Where("task_id = ?", taskID).First(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			conversation = model.Conversation{ID: uuid.V7(), Name: "处理详情", TaskID: &taskID, ActorKind: response.Kind, ActorKey: response.ActorKey}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		content, err := json.Marshal(map[string]any{
			"version": "1.0", "biz": "chat", "meta": map[string]any{"effect_key": request.Key}, "blocks": []any{},
		})
		if err != nil {
			return err
		}
		message = model.Message{ID: uuid.V7(), TaskID: taskID, ConversationID: conversation.ID,
			Kind: string(request.Actor.Kind), ActorKey: request.Actor.Key, Purpose: "output",
			OutputKey: &request.Key, Revision: 1, Content: content}
		return tx.Create(&message).Error
	})
	return
}

// SaveOutput records stream content without turning persistence time into output activity time.
func (store *Store) SaveOutput(ctx context.Context, id string, content []byte, revision uint64) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).Where("id = ?", id).
		UpdateColumns(map[string]any{"content": content, "revision": revision}).Error)
}
