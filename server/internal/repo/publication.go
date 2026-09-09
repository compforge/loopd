package repo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Speak serializes identity allocation on the conversation, not an input.
// The same logical output can be retried after its original UI stream closes.
func (store *Store) Speak(ctx context.Context, convID string, request contract.SpeakRequest) (result model.Message, err error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", convID).Error; err != nil {
			return mapError(err)
		}
		key := fmt.Sprintf("publish/%x", sha256.Sum256([]byte(convID+"\x00"+string(request.Actor.Kind)+"\x00"+request.Actor.Key+"\x00"+request.Key)))
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id = ? AND output_key = ?", convID, key).First(&result).Error
		if e == nil {
			if result.TargetKind != request.Target.Kind || result.TargetKey != request.Target.Key || result.ReplyToID != request.ReplyToID {
				return ErrConflict
			}
			return hydrateMessage(tx.Clauses(clause.Locking{Strength: "UPDATE"}), &result)
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
		if err := ensureParticipantConversation(tx, conv, request.Target); err != nil {
			return err
		}
		status := request.Status
		if status == "" {
			status = contract.MessageStatusCompleted
		}
		result = model.Message{ID: uuid.V7(), ConversationID: convID,
			SourceKind: request.Actor.Kind, SourceKey: request.Actor.Key, TargetKind: request.Target.Kind, TargetKey: request.Target.Key,
			ReplyToID: request.ReplyToID, OutputKey: &key, Revision: 1, Content: request.Content, Status: string(status),
			DispatchPending: request.Target.Kind != contract.ActorKindUser}
		return mapError(store.saveMessage(tx, &result, true))
	})
	return
}
