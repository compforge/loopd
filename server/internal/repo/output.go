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
	err = store.withChat(ctx, taskID, func(tx *gorm.DB, input, response model.Message) error {
		err := tx.Where("task_id = ? AND output_key = ?", taskID, request.Key).First(&message).Error
		if err == nil {
			if message.Kind != string(request.Actor.Kind) || message.ActorKey != request.Actor.Key ||
				(request.ConversationID != "" && message.ConversationID != request.ConversationID) ||
				(request.ConversationID == "" && message.ConversationID == input.ConversationID) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if input.DeliveryState != "" {
			return ErrConflict
		}
		var conversation model.Conversation
		if request.ConversationID != "" && request.ConversationID != input.ConversationID {
			return ErrConflict
		}
		if request.ConversationID == input.ConversationID {
			err = tx.First(&conversation, "id = ?", input.ConversationID).Error
		} else {
			err = tx.Where("task_id = ?", taskID).First(&conversation).Error
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			conversation = model.Conversation{ID: uuid.V7(), Name: "处理详情", TaskID: &taskID, ActorKind: input.TargetKind, ActorKey: input.TargetKey}
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
		if conversation.ID == input.ConversationID {
			message.TargetKind, message.TargetKey = input.Kind, input.ActorKey
		}
		return tx.Create(&message).Error
	})
	return
}

// EnsureResponse is the optional single-answer convenience, not a Chat prerequisite.
func (store *Store) EnsureResponse(ctx context.Context, taskID string) (message model.Message, err error) {
	err = store.withChat(ctx, taskID, func(tx *gorm.DB, input, _ model.Message) error {
		err := tx.Where("task_id = ? AND purpose = ?", taskID, "response").First(&message).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if input.DeliveryState != "" {
			return ErrConflict
		}
		message = model.Message{
			ID: uuid.V7(), ConversationID: input.ConversationID, TaskID: taskID,
			Kind: input.TargetKind, ActorKey: input.TargetKey, TargetKind: input.Kind, TargetKey: input.ActorKey,
			ReplyToMessageID: input.ID, Purpose: "response", Revision: 1,
			Content: []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`),
		}
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
