package component

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

type capacityAdapter struct {
	started chan string
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (a *capacityAdapter) Prompt(ctx context.Context, request harness.Request) (harness.Call, error) {
	n := a.active.Add(1)
	for old := a.peak.Load(); n > old; old = a.peak.Load() {
		if a.peak.CompareAndSwap(old, n) {
			break
		}
	}
	a.started <- request.CallID
	events := make(chan harness.Event)
	close(events)
	return &capacityCall{id: request.CallID, events: events, adapter: a}, nil
}

type capacityCall struct {
	id      string
	events  chan harness.Event
	adapter *capacityAdapter
}

func (c *capacityCall) ID() string                   { return c.id }
func (c *capacityCall) Events() <-chan harness.Event { return c.events }
func (c *capacityCall) Wait(ctx context.Context) (harness.Result, error) {
	defer c.adapter.active.Add(-1)
	select {
	case <-ctx.Done():
		return harness.Result{}, ctx.Err()
	case <-c.adapter.release:
		return harness.Result{Text: "done"}, nil
	}
}
func capacityFixture(t *testing.T, limit int) (*repo.Store, *HarnessRunner, *service.HarnessRunService, *capacityAdapter) {
	t.Helper()
	store, err := repo.Open(repo.Config{DSN: filepath.Join(t.TempDir(), "capacity.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateConversation(context.Background(), model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"}); err != nil {
		t.Fatal(err)
	}
	adapter := &capacityAdapter{started: make(chan string, 100), release: make(chan struct{})}
	adapters := map[string]harness.Adapter{"test": adapter}
	runner := NewHarnessRunner(store, adapters, nil)
	runner.Concurrency = limit
	runner.ScanInterval = 10 * time.Millisecond
	return store, runner, service.NewHarnessRunService(store, adapters, runner), adapter
}
func capacityRequest(key string) contract.HarnessRunRequest {
	return contract.HarnessRunRequest{ConversationID: "conv", IdempotencyKey: key, EffectKey: "work", Target: "test", Text: "go", Timeout: time.Minute, Actor: &contract.ActorRef{Kind: contract.ActorKindHarness, Key: "test"}, Meta: map[string]any{}}
}
func startCapacityRunner(t *testing.T, runner *HarnessRunner) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runner.Run(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Runner did not stop")
		}
	})
}
func waitCapacity(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("Harness condition did not converge")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// +case=`Simultaneous new submissions cannot exceed per-Server capacity, while same-key retries remain available at capacity and completed calls release their slots.`
func TestHarnessCapacityAdmissionAndRelease(t *testing.T) {
	store, runner, svc, adapter := capacityFixture(t, 3)
	ctx := context.Background()
	var wg sync.WaitGroup
	accepted := make(chan string, 20)
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			key := fmt.Sprint(i)
			if _, err := svc.Submit(ctx, capacityRequest(key)); err != nil {
				failures <- err
			} else {
				accepted <- key
			}
		})
	}
	wg.Wait()
	close(accepted)
	close(failures)
	if len(accepted) != 3 || len(failures) != 17 {
		t.Fatalf("accepted=%d rejected=%d", len(accepted), len(failures))
	}
	for err := range failures {
		if !errors.Is(err, ErrHarnessCapacity) {
			t.Fatal(err)
		}
	}
	key := <-accepted
	original, err := svc.Submit(ctx, capacityRequest(key))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := svc.Submit(ctx, capacityRequest(key))
	if err != nil || retry.ID != original.ID {
		t.Fatalf("retry=%+v %v", retry, err)
	}
	changed := capacityRequest(key)
	changed.Text = "changed"
	if _, err := svc.Submit(ctx, changed); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	messages, err := store.ListMessages(ctx, "conv", "", 100)
	if err != nil || len(messages) != 3 {
		t.Fatalf("messages=%d %v", len(messages), err)
	}
	runs, err := store.ListRunnableHarnessRuns(ctx, 100)
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs=%d %v", len(runs), err)
	}
	startCapacityRunner(t, runner)
	waitCapacity(t, func() bool { return adapter.active.Load() == 3 })
	if _, err := svc.Submit(ctx, capacityRequest("full")); !errors.Is(err, ErrHarnessCapacity) {
		t.Fatalf("running capacity=%v", err)
	}
	close(adapter.release)
	waitCapacity(t, func() bool { return runner.available() == 3 })
	if _, err := svc.Submit(ctx, capacityRequest("later")); err != nil {
		t.Fatal(err)
	}
	if adapter.peak.Load() > 3 {
		t.Fatalf("execution peak=%d", adapter.peak.Load())
	}
}

// +case=`A full Runner still finalizes expired and cancelled recovery records, preserves a live owner's lease, and publishes each Message end without starting those Harnesses.`
func TestHarnessMaintenanceWhileRecoveryUsesAllSlots(t *testing.T) {
	store, runner, svc, adapter := capacityFixture(t, 1)
	ctx := context.Background()
	// Persisted by an earlier process: recovery must consume the same capacity as submission.
	busy, err := store.CreateHarnessRun(ctx, capacityRequest("busy"), nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan string, 10)
	runner.Publish = func(_ context.Context, id string, event ui.Event) {
		if event.Op == ui.OpEnd {
			events <- id
		}
	}
	startCapacityRunner(t, runner)
	select {
	case id := <-adapter.started:
		if id != busy.ID {
			t.Fatal(id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery never started")
	}
	if _, err := svc.Submit(ctx, capacityRequest("new")); !errors.Is(err, ErrHarnessCapacity) {
		t.Fatalf("recovery did not reserve capacity: %v", err)
	}
	expiredRequest := capacityRequest("expired")
	expiredRequest.Timeout = time.Nanosecond
	expired, err := store.CreateHarnessRun(ctx, expiredRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CreateHarnessRun(ctx, capacityRequest("cancelled"), nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Lock(ctx, repo.HarnessResource(cancelled.ID), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	waitCapacity(t, func() bool {
		value, _ := store.GetHarnessRunState(ctx, expired.ID)
		return value.Phase == string(contract.CallTimedOut)
	})
	current, err := store.GetHarnessRunState(ctx, cancelled.ID)
	if err != nil || current.Phase != string(contract.CallPending) {
		t.Fatalf("live lease was overridden: %+v %v", current, err)
	}
	if err := store.Unlock(ctx, token); err != nil {
		t.Fatal(err)
	}
	waitCapacity(t, func() bool {
		value, _ := store.GetHarnessRunState(ctx, cancelled.ID)
		return value.Phase == string(contract.CallCancelled)
	})
	for id, want := range map[string]string{expired.MessageID: "failed", cancelled.MessageID: "cancelled"} {
		message, err := store.GetMessage(ctx, id)
		if err != nil || message.Status != want {
			t.Fatalf("message status=%s want=%s err=%v", message.Status, want, err)
		}
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-events:
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("missing published terminal event")
		}
	}
	if !seen[expired.MessageID] || !seen[cancelled.MessageID] {
		t.Fatalf("published=%v", seen)
	}
	current, err = store.GetHarnessRunState(ctx, busy.ID)
	if err != nil || contract.CallPhase(current.Phase).Terminal() || adapter.peak.Load() != 1 || adapter.active.Load() != 1 {
		t.Fatalf("busy=%+v active=%d err=%v", current, adapter.active.Load(), err)
	}
	if err := svc.Cancel(ctx, busy.ID); err != nil {
		t.Fatal(err)
	}
	waitCapacity(t, func() bool { return runner.available() == 1 })
	current, err = store.GetHarnessRunState(ctx, busy.ID)
	if err != nil || current.Phase != string(contract.CallCancelled) {
		t.Fatalf("busy cancellation=%+v %v", current, err)
	}
}

// A failure after reserving capacity must roll back both visible output and the local slot.
func TestHarnessAdmissionRollbackReleasesSlot(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "rollback.db")
	store, err := repo.Open(repo.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"}); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER fail_run BEFORE INSERT ON harness_runs BEGIN SELECT RAISE(ABORT, 'injected Run insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	runner := NewHarnessRunner(store, nil, nil)
	runner.Concurrency = 1
	if _, err := runner.Submit(ctx, capacityRequest("once")); err == nil || errors.Is(err, ErrHarnessCapacity) {
		t.Fatalf("injected failure=%v", err)
	}
	if runner.available() != 1 {
		t.Fatal("failed transaction leaked a reservation")
	}
	messages, err := store.ListMessages(ctx, "conv", "", 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("rolled-back message escaped: %v %v", messages, err)
	}
	if _, err := database.Exec(`DROP TRIGGER fail_run`); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Submit(ctx, capacityRequest("once")); err != nil {
		t.Fatalf("retry after rollback=%v", err)
	}
}
