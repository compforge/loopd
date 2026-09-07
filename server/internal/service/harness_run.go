package service

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type HarnessRunDriver interface {
	Submit(context.Context, contract.HarnessRunRequest) (model.HarnessRun, error)
	Wake()
}

type HarnessRunService struct {
	Messages *MessageService
	Listen   func(context.Context, string, func(ui.Event) error) error
	store    *repo.Store
	targets  map[string]harness.Adapter
	driver   HarnessRunDriver
}

func NewHarnessRunService(store *repo.Store, targets map[string]harness.Adapter, driver HarnessRunDriver) *HarnessRunService {
	return &HarnessRunService{store: store, targets: maps.Clone(targets), driver: driver}
}
func (s *HarnessRunService) Submit(ctx context.Context, r contract.HarnessRunRequest) (contract.HarnessCall, error) {
	if strings.TrimSpace(r.ConversationID) == "" || strings.TrimSpace(r.IdempotencyKey) == "" || r.EffectKey == "" || r.Target == "" || strings.TrimSpace(r.Text) == "" || r.Timeout < 0 {
		return contract.HarnessCall{}, ErrInvalid
	}
	if s.targets[r.Target] == nil {
		return contract.HarnessCall{}, fmt.Errorf("%w: Harness target %q is not configured", ErrInvalid, r.Target)
	}
	if r.Timeout == 0 {
		r.Timeout = 30 * time.Minute
	}
	if r.Actor == nil {
		r.Actor = &contract.ActorRef{Kind: contract.ActorKindHarness, Key: r.Target}
	}
	if !r.Actor.ValidTarget() || (r.Recipient != (contract.ActorRef{}) && !r.Recipient.Valid()) {
		return contract.HarnessCall{}, ErrInvalid
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	for _, key := range []string{"output", "human"} {
		if _, ok := r.Meta[key]; ok {
			return contract.HarnessCall{}, ErrInvalid
		}
	}
	run, err := s.driver.Submit(ctx, r)
	if err != nil {
		return contract.HarnessCall{}, err
	}
	// Initialize before handing out the Call so a normal observer attaches to
	// Redis immediately. DB acceptance remains valid during bridge outages.
	if s.Messages != nil {
		if err := s.Messages.ensureStream(ctx, model.Message{ID: run.MessageID}); err != nil {
			s.Messages.logger.WarnContext(ctx, "Harness stream initialization deferred", "run_id", run.ID, "error", err)
		}
	}
	s.driver.Wake()
	return harnessCall(run), nil
}
func harnessCall(run model.HarnessRun) contract.HarnessCall {
	return contract.HarnessCall{ID: run.ID, ConversationID: run.ConversationID, MessageID: run.MessageID, EffectKey: run.EffectKey, Target: run.Target, Phase: contract.CallPhase(run.Phase), Error: run.Error, DeadlineAt: run.DeadlineAt, Timestamped: contract.Timestamped{CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}}
}
func (s *HarnessRunService) Observe(ctx context.Context, id string) (contract.HarnessObservation, error) {
	run, err := s.store.GetHarnessRunState(ctx, id)
	if err != nil {
		return contract.HarnessObservation{}, err
	}
	message, err := s.store.GetMessage(ctx, run.MessageID)
	if err != nil {
		return contract.HarnessObservation{}, err
	}
	call := harnessCall(run)
	if call.Phase == contract.CallSucceeded {
		call.Result, err = contract.ExtractResult(message.Content)
		if err != nil {
			return contract.HarnessObservation{}, err
		}
		if call.Result == nil {
			return contract.HarnessObservation{}, fmt.Errorf("completed Harness run %s has no result", id)
		}
	}
	return contract.HarnessObservation{Call: call, Message: messageFromModel(message)}, nil
}
func (s *HarnessRunService) Cancel(ctx context.Context, id string) error {
	if err := s.store.CancelHarnessRun(ctx, id); err != nil {
		return err
	}
	s.driver.Wake()
	return nil
}

func (s *HarnessRunService) Stream(ctx context.Context, id string, deliver func(ui.Event) error) error {
	if s.Listen == nil {
		return ErrUnavailable
	}
	run, err := s.store.GetHarnessRunState(ctx, id)
	if err != nil {
		return err
	}
	return s.Listen(ctx, run.MessageID, deliver)
}

// Get reads execution metadata; message bodies are resolved through Message reads.
func (s *HarnessRunService) Get(ctx context.Context, id string) (contract.HarnessCall, error) {
	run, err := s.store.GetHarnessRunState(ctx, id)
	if err != nil {
		return contract.HarnessCall{}, err
	}
	return harnessCall(run), nil
}
