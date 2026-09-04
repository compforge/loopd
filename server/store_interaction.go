package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/compforge/loopd/api"
	"gorm.io/gorm"
)

func (store *Store) EnsureInteraction(
	ctx context.Context,
	invocationID string,
	request api.InteractionRequest,
) (api.Interaction, bool, error) {
	if request.OwnerUID == "" || request.EffectKey == "" || !request.Requester.Valid() ||
		(request.Kind != api.InteractionAsk && request.Kind != api.InteractionConfirm) ||
		strings.TrimSpace(request.Prompt) == "" {
		return api.Interaction{}, false, ErrInvalid
	}
	requestHash, optionsJSON, err := hashInteractionRequest(request)
	if err != nil {
		return api.Interaction{}, false, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row interactionPO
	created := false
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invocation invocationPO
		if err := tx.First(&invocation, "id = ?", invocationID).Error; err != nil {
			return mapDBError(err)
		}
		err := tx.Where("owner_uid = ? AND effect_key = ?", request.OwnerUID, request.EffectKey).First(&row).Error
		switch {
		case err == nil:
			if row.RequestHash != requestHash || row.InvocationID != invocationID {
				return ErrConflict
			}
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		if api.InvocationPhase(invocation.Phase).Terminal() {
			return ErrConflict
		}
		now := time.Now().UTC()
		row = interactionPO{
			ID:            newID("interaction"),
			InvocationID:  invocationID,
			OwnerUID:      request.OwnerUID,
			EffectKey:     request.EffectKey,
			RequestHash:   requestHash,
			RequesterRole: string(request.Requester.Role),
			RequesterID:   request.Requester.ID,
			Kind:          string(request.Kind),
			Title:         request.Title,
			Prompt:        strings.TrimSpace(request.Prompt),
			OptionsJSON:   optionsJSON,
			Phase:         string(api.InteractionPending),
			ExpiresAt:     request.ExpiresAt,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		created = true
		if err := tx.Model(&invocationPO{}).Where("id = ?", invocationID).
			Update("phase", api.InvocationWaitingInput).Error; err != nil {
			return err
		}
		return appendEvent(tx, invocationID, "", "interaction.created", interactionFromPO(row))
	})
	return interactionFromPO(row), created, err
}

func (store *Store) GetInteraction(ctx context.Context, id string) (api.Interaction, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row interactionPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.Interaction{}, mapDBError(err)
	}
	return interactionFromPO(row), nil
}

func (store *Store) ResolveInteraction(ctx context.Context, id, answer string) (api.Interaction, error) {
	if strings.TrimSpace(answer) == "" {
		return api.Interaction{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row interactionPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		if api.InteractionPhase(row.Phase) == api.InteractionResolved {
			if row.Answer == answer {
				return nil
			}
			return ErrConflict
		}
		if api.InteractionPhase(row.Phase) != api.InteractionPending {
			return ErrConflict
		}
		now := time.Now().UTC()
		row.Answer = answer
		row.Phase = string(api.InteractionResolved)
		row.ResolvedAt = &now
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		var pending int64
		if err := tx.Model(&interactionPO{}).
			Where("invocation_id = ? AND id <> ? AND phase = ?", row.InvocationID, row.ID, api.InteractionPending).
			Count(&pending).Error; err != nil {
			return err
		}
		if pending == 0 {
			if err := tx.Model(&invocationPO{}).
				Where("id = ? AND phase = ?", row.InvocationID, api.InvocationWaitingInput).
				Update("phase", api.InvocationRunning).Error; err != nil {
				return err
			}
		}
		return appendEvent(tx, row.InvocationID, "", "interaction.resolved", interactionFromPO(row))
	})
	return interactionFromPO(row), err
}
