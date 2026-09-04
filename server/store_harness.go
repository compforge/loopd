package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/compforge/loopd/api"
	"gorm.io/gorm"
)

func (store *Store) EnsureHarnessCall(
	ctx context.Context,
	invocationID string,
	request api.PromptRequest,
) (api.HarnessCall, bool, error) {
	if request.OwnerUID == "" || request.EffectKey == "" || request.Target == "" || strings.TrimSpace(request.Prompt) == "" {
		return api.HarnessCall{}, false, ErrInvalid
	}
	requestHash, toolsJSON, err := hashPromptRequest(request)
	if err != nil {
		return api.HarnessCall{}, false, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row harnessCallPO
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
		row = harnessCallPO{
			ID:           newID("call"),
			InvocationID: invocationID,
			OwnerUID:     request.OwnerUID,
			EffectKey:    request.EffectKey,
			RequestHash:  requestHash,
			Target:       request.Target,
			Prompt:       strings.TrimSpace(request.Prompt),
			ToolsJSON:    toolsJSON,
			Phase:        string(api.CallPending),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		created = true
		return appendEvent(tx, invocationID, row.ID, "harness_call.created", callFromPO(row))
	})
	return callFromPO(row), created, err
}

func (store *Store) GetHarnessCall(ctx context.Context, id string) (api.HarnessCall, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row harnessCallPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.HarnessCall{}, mapDBError(err)
	}
	return callFromPO(row), nil
}

func (store *Store) GetHarnessRequest(ctx context.Context, id string) (api.HarnessCall, api.PromptRequest, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row harnessCallPO
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return api.HarnessCall{}, api.PromptRequest{}, mapDBError(err)
	}
	var tools []api.Tool
	if len(row.ToolsJSON) > 0 {
		if err := json.Unmarshal(row.ToolsJSON, &tools); err != nil {
			return api.HarnessCall{}, api.PromptRequest{}, err
		}
	}
	return callFromPO(row), api.PromptRequest{
		OwnerUID: row.OwnerUID, EffectKey: row.EffectKey, Target: row.Target, Prompt: row.Prompt, Tools: tools,
	}, nil
}

func (store *Store) ListRunnableHarnessCalls(ctx context.Context, limit int) ([]api.HarnessCall, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []harnessCallPO
	err := store.db.WithContext(ctx).
		Where("phase IN ?", []api.CallPhase{api.CallPending, api.CallStarting, api.CallRunning, api.CallWaitingInput}).
		Order("updated_at ASC").
		Limit(normalizeLimit(limit)).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]api.HarnessCall, 0, len(rows))
	for _, row := range rows {
		result = append(result, callFromPO(row))
	}
	return result, nil
}

func (store *Store) UpdateHarnessCall(ctx context.Context, id string, update HarnessCallUpdate) (api.HarnessCall, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var row harnessCallPO
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return mapDBError(err)
		}
		changed := row.Phase != string(update.Phase) ||
			(update.ExternalRef != "" && row.ExternalRef != update.ExternalRef) ||
			(update.Result != "" && row.Result != update.Result) ||
			row.Error != update.Error
		row.Phase = string(update.Phase)
		if update.ExternalRef != "" {
			row.ExternalRef = update.ExternalRef
		}
		if update.ProviderCursor != "" {
			row.ProviderCursor = update.ProviderCursor
		}
		if update.Result != "" {
			row.Result = update.Result
		}
		row.Error = update.Error
		if !update.ActivityAt.IsZero() {
			at := update.ActivityAt.UTC()
			row.LastActivityAt = &at
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return appendEvent(tx, row.InvocationID, row.ID, "harness_call.updated", callFromPO(row))
	})
	return callFromPO(row), err
}

type HarnessCallUpdate struct {
	Phase          api.CallPhase
	ExternalRef    string
	ProviderCursor string
	ActivityAt     time.Time
	Result         string
	Error          string
}

func (store *Store) AppendHarnessEvent(ctx context.Context, callID, providerCursor, kind string, data json.RawMessage) (api.HarnessEvent, error) {
	if kind == "" {
		return api.HarnessEvent{}, ErrInvalid
	}
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var result api.HarnessEvent
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var call harnessCallPO
		if err := tx.First(&call, "id = ?", callID).Error; err != nil {
			return mapDBError(err)
		}
		if providerCursor != "" {
			var existing invocationEventPO
			err := tx.Where("call_id = ? AND provider_cursor = ?", callID, providerCursor).First(&existing).Error
			if err == nil {
				result = api.HarnessEvent{
					Cursor: existing.Cursor, ProviderCursor: existing.ProviderCursor,
					CallID: call.ID, InvocationID: call.InvocationID,
					Kind: strings.TrimPrefix(existing.Kind, "harness."),
					Data: json.RawMessage(existing.Data), CreatedAt: existing.CreatedAt,
				}
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		row := invocationEventPO{
			InvocationID:   call.InvocationID,
			CallID:         call.ID,
			ProviderCursor: providerCursor,
			Kind:           "harness." + kind,
			Data:           append([]byte(nil), data...),
			CreatedAt:      time.Now().UTC(),
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		now := row.CreatedAt
		call.LastEventCursor = row.Cursor
		call.LastActivityAt = &now
		if kind == "message.delta" {
			call.StreamText += harnessTextDelta(data)
		}
		if err := tx.Save(&call).Error; err != nil {
			return err
		}
		result = api.HarnessEvent{
			Cursor: row.Cursor, ProviderCursor: row.ProviderCursor,
			CallID: call.ID, InvocationID: call.InvocationID,
			Kind: kind, Data: append(json.RawMessage(nil), data...), CreatedAt: row.CreatedAt,
		}
		return nil
	})
	return result, err
}

func (store *Store) ListHarnessEvents(ctx context.Context, callID string, after uint64, limit int) ([]api.HarnessEvent, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []invocationEventPO
	if err := store.db.WithContext(ctx).
		Where("call_id = ? AND cursor > ? AND kind LIKE ?", callID, after, "harness.%").
		Order("cursor ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]api.HarnessEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, api.HarnessEvent{
			Cursor: row.Cursor, ProviderCursor: row.ProviderCursor,
			CallID: row.CallID, InvocationID: row.InvocationID,
			Kind: strings.TrimPrefix(row.Kind, "harness."), Data: json.RawMessage(row.Data), CreatedAt: row.CreatedAt,
		})
	}
	return result, nil
}
