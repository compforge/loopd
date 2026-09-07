// Package component owns Server background components and their lifecycle.
package component

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/internal/lock"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type HarnessRunner struct {
	Publish      func(context.Context, string, ui.Event)
	store        *repo.Store
	adapters     map[string]harness.Adapter
	logger       *slog.Logger
	wake         chan struct{}
	Concurrency  int
	LeaseTTL     time.Duration
	ScanInterval time.Duration
}

func NewHarnessRunner(store *repo.Store, adapters map[string]harness.Adapter, logger *slog.Logger) *HarnessRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &HarnessRunner{store: store, adapters: maps.Clone(adapters), logger: logger, wake: make(chan struct{}, 1), Concurrency: 8, LeaseTTL: 30 * time.Second, ScanInterval: time.Second}
}
func (r *HarnessRunner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run bounds both active executions and pending goroutines; each lease belongs
// to one Run, not a global leader or an Operator process.
func (r *HarnessRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.ScanInterval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	finished := make(chan string, r.Concurrency)
	active := map[string]bool{}
	r.Wake()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-finished:
			delete(active, id)
			r.Wake()
		case <-ticker.C:
			r.Wake()
		case <-r.wake:
			if len(active) >= r.Concurrency {
				continue
			}
			runs, err := r.store.ListRunnableHarnessRuns(ctx, r.Concurrency)
			if err != nil {
				r.logger.ErrorContext(ctx, "scan Harness runs", "error", err)
				continue
			}
			for _, run := range runs {
				if active[run.ID] || len(active) >= r.Concurrency {
					continue
				}
				active[run.ID] = true
				wg.Add(1)
				go func(run model.HarnessRun) {
					defer wg.Done()
					defer func() { finished <- run.ID }()
					err := r.Drive(ctx, run.ID)
					if err != nil && !errors.Is(err, lock.ErrLocked) && !errors.Is(err, context.Canceled) {
						r.logger.WarnContext(ctx, "drive Harness run", "run_id", run.ID, "error", err)
						timer := time.NewTimer(time.Second)
						select {
						case <-ctx.Done():
							timer.Stop()
						case <-timer.C:
						}
					}
				}(run)
			}
		}
	}
}
func (r *HarnessRunner) Drive(ctx context.Context, id string) error {
	return lock.WithLease(ctx, r.store, repo.HarnessResource(id), r.LeaseTTL, func(owned context.Context, token lock.Token) error {
		run, err := r.store.StartHarnessRun(owned, token, id)
		if err != nil {
			return err
		}
		if run.CancelRequested {
			return r.finish(owned, token, run, contract.CallCancelled, nil, "cancelled")
		}
		if !time.Now().Before(run.DeadlineAt) {
			return r.finish(owned, token, run, contract.CallTimedOut, nil, "deadline exceeded")
		}
		var input contract.HarnessRunRequest
		if err := json.Unmarshal(run.Request, &input); err != nil {
			return err
		}
		adapter := r.adapters[run.Target]
		if adapter == nil {
			return r.finish(owned, token, run, contract.CallUnknown, nil, "Harness target is not configured on this server")
		}
		observation, stop := context.WithDeadline(owned, run.DeadlineAt)
		defer stop()
		// Cancellation is observed through SQL on any replica; stopping local
		// observation does not assert that remote execution was interrupted.
		watcherDone := make(chan struct{})
		defer func() { stop(); <-watcherDone }()
		go func() {
			defer close(watcherDone)
			ticker := time.NewTicker(r.ScanInterval)
			defer ticker.Stop()
			for {
				select {
				case <-observation.Done():
					return
				case <-ticker.C:
					current, e := r.store.GetHarnessRunState(observation, id)
					if e == nil && current.CancelRequested {
						stop()
						return
					}
				}
			}
		}()
		request := harness.Request{CallID: id, IdempotencyKey: id, ScopeKey: input.Actor.Key, EffectKey: input.EffectKey, ExecutionRef: run.ExecutionRef, Timeout: time.Until(run.DeadlineAt), Prompt: input.Text, Tools: input.Tools}
		var call harness.Call
		if run.Phase == string(contract.CallPending) {
			call, err = adapter.Prompt(observation, request)
		} else if recoverable, ok := adapter.(harness.Recoverable); ok {
			call, err = recoverable.Resume(observation, request)
		} else {
			err = harness.ErrRecoveryUnsupported
		}
		if err != nil {
			return r.finishError(owned, token, run, err)
		}
		if call == nil || call.ID() != id {
			return r.finishError(owned, token, run, errors.New("Adapter returned an invalid Call identity"))
		}
		ref := ""
		if locator, ok := call.(harness.ExecutionReference); ok {
			ref = locator.ExecutionRef()
		}
		if err := r.store.BindHarnessExecution(owned, token, id, ref); err != nil {
			return err
		}
		index := uint64(0)
		replayHash := ""
		for {
			select {
			case <-observation.Done():
				return r.finishError(owned, token, run, observation.Err())
			case event, ok := <-call.Events():
				if !ok {
					result, err := call.Wait(observation)
					if err != nil {
						return r.finishError(owned, token, run, err)
					}
					if index < run.Checkpoint {
						return r.finishError(owned, token, run, errors.New("Harness replay ended before persisted checkpoint"))
					}
					value := result.Value()
					if err := value.Validate(); err != nil {
						return r.finishError(owned, token, run, fmt.Errorf("invalid Harness result: %w", err))
					}
					return r.finish(owned, token, run, contract.CallSucceeded, &value, "")
				}
				index++
				replayHash = fmt.Sprintf("%x", sha256.Sum256(append([]byte(replayHash), event.Data...)))
				if index <= run.Checkpoint {
					if index == run.Checkpoint && replayHash != run.ReplayHash {
						return r.finishError(owned, token, run, errors.New("Harness replay differs from persisted output"))
					}
					continue
				}
				value, err := ui.Parse(event.Data)
				if err != nil {
					return r.finishError(owned, token, run, err)
				}
				if err := r.store.AppendHarnessOutput(observation, token, id, index, replayHash, value); err != nil {
					if errors.Is(err, context.Canceled) || errors.Is(err, repo.ErrInvalidContent) {
						return r.finishError(owned, token, run, err)
					}
					// Ambiguous storage writes remain recoverable at the persisted checkpoint.
					return err
				}
				value.Seq = index + 1
				r.publish(owned, run.MessageID, value)
			}
		}
	})
}
func (r *HarnessRunner) finishError(ctx context.Context, t lock.Token, run model.HarnessRun, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	phase := contract.CallFailed
	if errors.Is(err, harness.ErrRecoveryUnsupported) {
		phase = contract.CallUnknown
	}
	if errors.Is(err, context.DeadlineExceeded) {
		phase = contract.CallTimedOut
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, repo.ErrInvalidContent) {
		phase = contract.CallCancelled
	}
	var observation *harness.ObservationError
	if errors.As(err, &observation) {
		return r.store.RetryHarnessRun(ctx, t, run.ID, err.Error())
	}
	return r.finish(ctx, t, run, phase, nil, err.Error())
}

func (r *HarnessRunner) publish(ctx context.Context, id string, event ui.Event) {
	if r.Publish == nil {
		return
	}
	publish, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r.Publish(publish, id, event)
}

func (r *HarnessRunner) finish(ctx context.Context, t lock.Token, run model.HarnessRun, phase contract.CallPhase, result *contract.HarnessResult, detail string) error {
	events, err := r.store.FinishHarnessRun(ctx, t, run.ID, phase, result, detail)
	if err != nil {
		return err
	}
	for _, event := range events {
		r.publish(ctx, run.MessageID, event)
	}
	return nil
}
