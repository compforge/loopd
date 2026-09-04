package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/compforge/loopd/api"
	"github.com/compforge/loopd/harness"
)

type RunnerConfig struct {
	PollInterval   time.Duration
	CallTimeout    time.Duration
	MaxConcurrency int
}

type Runner struct {
	store    *Store
	adapters map[string]harness.Adapter
	logger   *slog.Logger
	config   RunnerConfig
	wake     chan struct{}
	activeMu sync.Mutex
	active   map[string]struct{}
}

func NewRunner(store *Store, adapters map[string]harness.Adapter, logger *slog.Logger, config RunnerConfig) *Runner {
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.CallTimeout <= 0 {
		config.CallTimeout = 30 * time.Second
	}
	if config.MaxConcurrency <= 0 {
		config.MaxConcurrency = 8
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		store: store, adapters: adapters, logger: logger, config: config,
		wake: make(chan struct{}, 1), active: make(map[string]struct{}),
	}
}

func (runner *Runner) Notify() {
	select {
	case runner.wake <- struct{}{}:
	default:
	}
}

// Run only owns short provider observations. Harness executions themselves are
// provider-owned and durable, so restarting loop-server merely resumes polling.
func (runner *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(runner.config.PollInterval)
	defer ticker.Stop()
	runner.dispatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runner.dispatch(ctx)
		case <-runner.wake:
			runner.dispatch(ctx)
		}
	}
}

func (runner *Runner) dispatch(ctx context.Context) {
	calls, err := runner.store.ListRunnableHarnessCalls(ctx, runner.config.MaxConcurrency)
	if err != nil {
		runner.logger.Error("list runnable Harness Calls", "error", err)
		return
	}
	for _, call := range calls {
		if !runner.claim(call.ID) {
			continue
		}
		go func(call api.HarnessCall) {
			defer runner.release(call.ID)
			runner.advance(ctx, call)
		}(call)
	}
}

func (runner *Runner) claim(id string) bool {
	runner.activeMu.Lock()
	defer runner.activeMu.Unlock()
	if len(runner.active) >= runner.config.MaxConcurrency {
		return false
	}
	if _, exists := runner.active[id]; exists {
		return false
	}
	runner.active[id] = struct{}{}
	return true
}

func (runner *Runner) release(id string) {
	runner.activeMu.Lock()
	delete(runner.active, id)
	runner.activeMu.Unlock()
}

func (runner *Runner) advance(parent context.Context, call api.HarnessCall) {
	adapter, ok := runner.adapters[call.Target]
	if !ok {
		reason := fmt.Sprintf("Harness %q is not configured", call.Target)
		runner.finishCall(parent, call, HarnessCallUpdate{Phase: api.CallFailed, Error: reason})
		return
	}
	current, prompt, err := runner.store.GetHarnessRequest(parent, call.ID)
	if err != nil {
		runner.logger.Error("load Harness Call", "call_id", call.ID, "error", err)
		return
	}
	request := harness.Request{
		CallID: current.ID, IdempotencyKey: current.OwnerUID + "/" + current.EffectKey,
		ExternalRef: current.ExternalRef, Cursor: current.ProviderCursor,
		Target: prompt.Target, Prompt: prompt.Prompt, Tools: prompt.Tools,
	}
	emit := func(ctx context.Context, event harness.Event) error {
		_, err := runner.store.AppendHarnessEvent(ctx, current.ID, event.Cursor, event.Kind, event.Data)
		return err
	}
	ctx, cancel := context.WithTimeout(parent, runner.config.CallTimeout)
	defer cancel()

	var observation harness.Observation
	if current.Phase == api.CallPending || current.Phase == api.CallStarting {
		_, _ = runner.store.UpdateHarnessCall(ctx, current.ID, HarnessCallUpdate{
			Phase: api.CallStarting, ExternalRef: current.ExternalRef,
			ProviderCursor: current.ProviderCursor, ActivityAt: time.Now().UTC(),
		})
		observation, err = adapter.Ensure(ctx, request, emit)
	} else {
		observation, err = adapter.Observe(ctx, request, emit)
	}
	if err != nil {
		if harness.IsOutcomeUnknown(err) {
			runner.finishCall(parent, current, HarnessCallUpdate{
				Phase: api.CallUnknown, ExternalRef: observation.ExternalRef,
				ProviderCursor: observation.Cursor, Error: err.Error(),
			})
			return
		}
		// A transport observation failure is not a Harness failure. Persist the
		// error as activity and retry the same durable Call on the next poll.
		runner.logger.Warn("observe Harness Call", "call_id", current.ID, "error", err)
		_, _ = runner.store.AppendHarnessEvent(parent, current.ID, "", "observation.error", []byte(fmt.Sprintf(`{"message":%q}`, err.Error())))
		return
	}
	update := HarnessCallUpdate{
		Phase: observation.Phase, ExternalRef: observation.ExternalRef,
		ProviderCursor: observation.Cursor,
		ActivityAt:     observation.ActivityAt, Result: observation.Result, Error: observation.Error,
	}
	if update.Phase == "" {
		update.Phase = api.CallRunning
	}
	runner.finishCall(parent, current, update)
}

func (runner *Runner) finishCall(ctx context.Context, old api.HarnessCall, update HarnessCallUpdate) {
	call, err := runner.store.UpdateHarnessCall(ctx, old.ID, update)
	if err != nil {
		runner.logger.Error("persist Harness Call observation", "call_id", old.ID, "error", err)
		return
	}
	if !call.Phase.Terminal() {
		return
	}
	invocation, err := runner.store.GetInvocation(ctx, call.InvocationID)
	if err != nil {
		runner.logger.Error("load Harness Call Invocation", "call_id", call.ID, "error", err)
		return
	}
	// Calls owned by an Operator are internal evidence. Only a directly
	// addressed Harness may publish the Invocation's final chat answer.
	if invocation.Responder.Role != api.RoleHarness {
		return
	}
	if call.Phase == api.CallSucceeded {
		answer := call.Result
		if answer == "" {
			answer = call.StreamText
		}
		if _, err := runner.store.CompleteInvocation(ctx, invocation.ID, api.RoleHarness, invocation.Responder.ID, answer); err != nil {
			runner.logger.Error("publish Harness answer", "call_id", call.ID, "error", err)
		}
		return
	}
	reason := call.Error
	if reason == "" {
		reason = "Harness Call ended without a result"
	}
	if err := runner.store.FailInvocation(ctx, invocation.ID, reason); err != nil {
		runner.logger.Error("fail Harness Invocation", "call_id", call.ID, "error", err)
	}
}
