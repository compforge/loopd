package repo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/lock"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func HarnessResource(id string) string { return "harness-run/" + id }

// Run ownership is a stored relation, never inferred from an Actor's name.
// Call while holding the Message lock; Run and Message are created atomically.
func requireUnownedMessage(tx *gorm.DB, id string) error {
	var count int64
	if err := tx.Model(&model.HarnessRun{}).Where("message_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	return nil
}

// CreateHarnessRun checks idempotency before calling admit for a new Run.
// The caller releases any reservation if the transaction fails. Replays never
// reserve capacity, even when this Server is full.
func (s *Store) CreateHarnessRun(ctx context.Context, request contract.HarnessRunRequest, admit func(string) error) (run model.HarnessRun, err error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return run, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(raw))
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(request.ConversationID+"\x00"+string(request.Actor.Kind)+"\x00"+request.Actor.Key+"\x00"+request.IdempotencyKey)))
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conv model.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "id = ?", request.ConversationID).Error; err != nil {
			return err
		}
		e := tx.Where("submission_key = ?", key).First(&run).Error
		if e == nil {
			if run.RequestHash != fingerprint {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if err := ensureParticipantConversation(tx, conv, request.Recipient); err != nil {
			return err
		}
		now := time.Now().UTC()
		run = model.HarnessRun{ID: uuid.V7(), MessageID: uuid.V7(), ConversationID: request.ConversationID, Target: request.Target, EffectKey: request.EffectKey, Request: raw, RequestHash: fingerprint, SubmissionKey: key, Phase: string(contract.CallPending), DeadlineAt: now.Add(request.Timeout), NextAttemptAt: now}
		if admit != nil {
			if err := admit(run.ID); err != nil {
				return err
			}
		}
		content, _ := json.Marshal(map[string]any{"version": "1.1", "biz": "chat", "meta": request.Meta, "blocks": []any{}})
		m := model.Message{ID: run.MessageID, ConversationID: run.ConversationID, Kind: request.Actor.Kind, ActorKey: request.Actor.Key, TargetKind: request.Recipient.Kind, TargetKey: request.Recipient.Key, DispatchPending: request.Recipient.Kind != contract.ActorKindUser, Revision: 1, Status: string(contract.MessageStatusStreaming), Content: content}
		if err := s.saveMessage(tx, &m, true); err != nil {
			return err
		}
		return tx.Create(&run).Error
	})
	return run, mapError(err)
}
func (s *Store) GetHarnessRun(ctx context.Context, id string) (run model.HarnessRun, err error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	err = mapError(s.db.WithContext(ctx).First(&run, "id = ?", id).Error)
	return
}
func (s *Store) ListRunnableHarnessRuns(ctx context.Context, limit int) ([]model.HarnessRun, error) {
	return s.listAvailableHarnessRuns(ctx, limit, false)
}

// ListDueHarnessRuns ignores execution retry delays; cancellation and deadlines
// must progress even when no execution slot is available.
func (s *Store) ListDueHarnessRuns(ctx context.Context, limit int) ([]model.HarnessRun, error) {
	return s.listAvailableHarnessRuns(ctx, limit, true)
}

func (s *Store) listAvailableHarnessRuns(ctx context.Context, limit int, due bool) ([]model.HarnessRun, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var rows []model.HarnessRun
	join := "resource_locks.resource = CONCAT('harness-run/', harness_runs.id)"
	if s.db.Dialector.Name() == "sqlite" {
		join = "resource_locks.resource = 'harness-run/' || harness_runs.id"
	}
	now := time.Now().UTC()
	query := s.db.WithContext(ctx).Model(&model.HarnessRun{}).Select("harness_runs.id").Joins("LEFT JOIN resource_locks ON "+join).
		Where("harness_runs.phase IN ?", []string{"pending", "starting", "running"}).
		Where("resource_locks.resource IS NULL OR resource_locks.expires_at <= ?", now)
	if due {
		query = query.Where("harness_runs.cancel_requested = ? OR harness_runs.deadline_at <= ?", true, now).Order("harness_runs.deadline_at, harness_runs.id")
	} else {
		query = query.Where("harness_runs.next_attempt_at <= ?", now).Order("harness_runs.next_attempt_at, harness_runs.id")
	}
	err := query.Limit(limit).Find(&rows).Error
	return rows, err
}

// harnessTransaction always locks lease -> run -> message, shared by all writers.
func (s *Store) harnessTransaction(ctx context.Context, t lock.Token, id string, fn func(*gorm.DB, *model.HarnessRun) error) error {
	if t.Resource != HarnessResource(id) {
		return lock.ErrLost
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return mapError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := fence(tx, t); err != nil {
			return err
		}
		var run model.HarnessRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", id).Error; err != nil {
			return err
		}
		if contract.CallPhase(run.Phase).Terminal() {
			return ErrConflict
		}
		return fn(tx, &run)
	}))
}
func (s *Store) StartHarnessRun(ctx context.Context, t lock.Token, id string) (run model.HarnessRun, err error) {
	err = s.harnessTransaction(ctx, t, id, func(tx *gorm.DB, current *model.HarnessRun) error {
		run = *current
		return tx.Model(current).Updates(map[string]any{"phase": string(contract.CallStarting), "next_attempt_at": time.Now().UTC()}).Error
	})
	return
}
func (s *Store) BindHarnessExecution(ctx context.Context, t lock.Token, id, ref string) error {
	return s.harnessTransaction(ctx, t, id, func(tx *gorm.DB, run *model.HarnessRun) error {
		if run.ExecutionRef != "" && run.ExecutionRef != ref {
			return ErrConflict
		}
		return tx.Model(run).Updates(map[string]any{"execution_ref": ref, "phase": string(contract.CallRunning), "error": ""}).Error
	})
}
func (s *Store) AppendHarnessOutput(ctx context.Context, t lock.Token, id string, index uint64, replayHash string, event ui.Event) error {
	return s.harnessTransaction(ctx, t, id, func(tx *gorm.DB, run *model.HarnessRun) error {
		if run.CancelRequested {
			return context.Canceled
		}
		if index <= run.Checkpoint {
			if index == run.Checkpoint && replayHash != run.ReplayHash {
				return ErrConflict
			}
			return nil
		}
		if index != run.Checkpoint+1 {
			return ErrConflict
		}
		if event.Op != ui.OpSet && event.Op != ui.OpAppend {
			return ErrInvalidContent
		}
		if event.Block["type"] == contract.ResultBlockType || event.Block["id"] == "result" {
			return fmt.Errorf("%w: result is published only at completion", ErrInvalidContent)
		}
		if event.Block["type"] == "ask" || event.Block["type"] == "confirm" || event.Block["type"] == "human_reply" || event.Meta != nil {
			return ErrInvalidContent
		}
		var m model.Message
		if err := tx.First(&m, "id = ?", run.MessageID).Error; err != nil {
			return err
		}
		event.Seq = m.Revision + 1
		if err := s.projectOutput(tx, m.ID, event, "", true); err != nil {
			return err
		}
		return tx.Model(run).Updates(map[string]any{"checkpoint": index, "replay_hash": replayHash}).Error
	})
}

// FinishHarnessRun commits the final block and terminal status atomically. A
// reader can never observe succeeded with an uncommitted result.
func (s *Store) FinishHarnessRun(ctx context.Context, t lock.Token, id string, phase contract.CallPhase, result *contract.HarnessResult, detail string) (events []ui.Event, err error) {
	if !phase.Terminal() {
		return nil, ErrConflict
	}
	err = s.harnessTransaction(ctx, t, id, func(tx *gorm.DB, run *model.HarnessRun) error {
		if run.CancelRequested {
			phase = contract.CallCancelled
			result = nil
			detail = "cancelled"
		} else if !time.Now().Before(run.DeadlineAt) {
			phase = contract.CallTimedOut
			result = nil
			detail = "Harness run deadline exceeded"
		}
		var m model.Message
		if err := tx.First(&m, "id = ?", run.MessageID).Error; err != nil {
			return err
		}
		if phase == contract.CallSucceeded {
			if result == nil {
				return ErrInvalidContent
			}
			block, err := result.Block()
			if err != nil {
				return err
			}
			event := ui.Event{Op: ui.OpSet, Seq: m.Revision + 1, Block: block}
			events = append(events, event)
			if err := s.projectOutput(tx, m.ID, event, "", true); err != nil {
				return err
			}
			m.Revision++
		}
		if phase != contract.CallSucceeded {
			event := ui.Event{Op: ui.OpSet, Seq: m.Revision + 1, Mask: "meta.error", Meta: map[string]any{"error": map[string]any{"code": string(phase), "message": detail}}}
			events = append(events, event)
			if err := s.projectOutput(tx, m.ID, event, "", true); err != nil {
				return err
			}
			m.Revision++
		}
		status := contract.MessageStatusFailed
		if phase == contract.CallSucceeded {
			status = contract.MessageStatusCompleted
		}
		if phase == contract.CallCancelled {
			status = contract.MessageStatusCancelled
		}
		events = append(events, ui.End(m.Revision+1))
		if err := s.projectOutput(tx, m.ID, events[len(events)-1], status, true); err != nil {
			return err
		}
		return tx.Model(run).Updates(map[string]any{"phase": string(phase), "error": detail}).Error
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) RetryHarnessRun(ctx context.Context, t lock.Token, id, detail string) error {
	return s.harnessTransaction(ctx, t, id, func(tx *gorm.DB, run *model.HarnessRun) error {
		return tx.Model(run).Updates(map[string]any{"error": detail, "next_attempt_at": time.Now().UTC().Add(time.Second)}).Error
	})
}
func (s *Store) CancelHarnessRun(ctx context.Context, id string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return mapError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run model.HarnessRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&run, "id = ?", id).Error; err != nil {
			return err
		}
		if contract.CallPhase(run.Phase).Terminal() {
			return nil
		}
		return tx.Model(&run).Updates(map[string]any{"cancel_requested": true, "next_attempt_at": time.Now().UTC()}).Error
	}))
}

// GetHarnessRunState avoids loading the saved prompt during observation/heartbeat.
func (s *Store) GetHarnessRunState(ctx context.Context, id string) (run model.HarnessRun, err error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	err = mapError(s.db.WithContext(ctx).Omit("request").First(&run, "id = ?", id).Error)
	return
}
