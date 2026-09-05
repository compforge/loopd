package repo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Speak serializes identity allocation on the conversation, not an input.
// The same logical output can be retried after its original UI stream closes.
func (store *Store) Speak(ctx context.Context, convID string, request loopd.SpeakRequest) (result model.Message, err error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", convID).Error; err != nil {
			return mapError(err)
		}
		key := fmt.Sprintf("publish/%x", sha256.Sum256([]byte(convID+"\x00"+string(request.Actor.Kind)+"\x00"+request.Actor.Key+"\x00"+request.Key)))
		e := tx.Where("conversation_id = ? AND output_key = ?", convID, key).First(&result).Error
		if e == nil {
			if result.TargetKind != string(request.Target.Kind) || result.TargetKey != request.Target.Key || result.ReplyToID != request.ReplyToID {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if request.ReplyToID != "" {
			var ref model.Message
			if err := tx.First(&ref, "id = ? AND conversation_id = ?", request.ReplyToID, convID).Error; err != nil {
				return mapError(err)
			}
		}
		var snapshot map[string]any
		if err := json.Unmarshal(request.Content, &snapshot); err != nil {
			return err
		}
		meta, _ := snapshot["meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
			snapshot["meta"] = meta
		}
		meta["output"] = map[string]any{"ended": !request.Stream}
		content, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		result = model.Message{ID: uuid.V7(), ConversationID: convID,
			Kind: string(request.Actor.Kind), ActorKey: request.Actor.Key, TargetKind: string(request.Target.Kind), TargetKey: request.Target.Key,
			ReplyToID: request.ReplyToID, Purpose: "output", OutputKey: &key, Revision: 1, Content: content,
			DispatchPending: !request.Stream && request.Target.Kind != loopd.ActorKindUser}
		return mapError(tx.Create(&result).Error)
	})
	return
}

// EnsureActorConversation lazily allocates one workspace per parent and actor.
// Parent locking serializes allocation across server replicas without a new table.
func (store *Store) EnsureActorConversation(ctx context.Context, parentID string, actor loopd.ActorRef) (result model.Conversation, err error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parent model.Conversation
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, "id = ?", parentID).Error; e != nil {
			return mapError(e)
		}
		e := tx.Where("parent_id = ? AND actor_kind = ? AND actor_key = ?", parentID, actor.Kind, actor.Key).Order("id ASC").First(&result).Error
		if e == nil {
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		result = model.Conversation{ID: uuid.V7(), Name: "处理详情", ParentID: &parentID, ActorKind: string(actor.Kind), ActorKey: actor.Key}
		return tx.Create(&result).Error
	})
	return
}
