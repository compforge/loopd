package component

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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

type replayAdapter struct {
	starts, resumes atomic.Int32
	changed         bool
	lost            chan struct{}
}

func (a *replayAdapter) Prompt(ctx context.Context, r harness.Request) (harness.Call, error) {
	a.starts.Add(1)
	c := a.call(r.CallID, false)
	go func() { <-ctx.Done(); close(a.lost) }()
	return c, nil
}
func (a *replayAdapter) Resume(_ context.Context, r harness.Request) (harness.Call, error) {
	a.resumes.Add(1)
	if r.ExecutionRef != "native-execution" {
		return nil, errors.New("lost native execution reference")
	}
	return a.call(r.CallID, true), nil
}
func (a *replayAdapter) call(id string, resume bool) *replayCall {
	c := &replayCall{id: id, events: make(chan harness.Event, 2)}
	text := "hello"
	if resume && a.changed {
		text = "changed"
	}
	for _, part := range []string{text, " world"} {
		data, _ := (ui.Event{Op: ui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "text", "type": "text", "content": part}}).Marshal()
		c.events <- harness.Event{Data: data}
		if !resume {
			break
		}
	}
	if resume {
		close(c.events)
	}
	return c
}

type replayCall struct {
	id     string
	events chan harness.Event
}

func (c *replayCall) ID() string                   { return c.id }
func (c *replayCall) ExecutionRef() string         { return "native-execution" }
func (c *replayCall) Events() <-chan harness.Event { return c.events }
func (c *replayCall) Wait(context.Context) (harness.Result, error) {
	return harness.Result{JSON: json.RawMessage(`{"ok":true}`)}, nil
}
func TestRunnerReattachesAndRejectsChangedReplay(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "changed"}[changed], func(t *testing.T) {
			store, err := repo.Open(repo.Config{DSN: filepath.Join(t.TempDir(), "run.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			_, err = store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"})
			if err != nil {
				t.Fatal(err)
			}
			adapter := &replayAdapter{changed: changed, lost: make(chan struct{})}
			adapters := map[string]harness.Adapter{"test": adapter}
			svc := service.NewHarnessRunService(store, adapters, NewHarnessRunner(store, adapters, nil))
			run, err := svc.Submit(ctx, contract.HarnessRunRequest{ConversationID: "conv", IdempotencyKey: "once", EffectKey: "work", Target: "test", Text: "go", Timeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			first := NewHarnessRunner(store, adapters, nil)
			first.ScanInterval = time.Millisecond * 10
			process, stop := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- first.Drive(process, run.ID) }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				current, _ := store.GetHarnessRun(ctx, run.ID)
				if current.Checkpoint == 1 {
					break
				}
				if time.Now().After(deadline) {
					stop()
					t.Fatal("first output not saved")
				}
				time.Sleep(time.Millisecond)
			}
			stop()
			<-done
			<-adapter.lost
			second := NewHarnessRunner(store, adapters, nil)
			if err := second.Drive(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			value, err := svc.Observe(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				if value.Call.Phase != contract.CallFailed || !strings.Contains(value.Call.Error, "replay differs") {
					t.Fatalf("changed replay=%+v", value.Call)
				}
			} else {
				if value.Call.Phase != contract.CallSucceeded || value.Call.Result.Text() != `{"ok":true}` {
					t.Fatalf("restored=%+v", value.Call)
				}
				if strings.Count(string(value.Message.Content), "hello world") != 1 {
					t.Fatalf("duplicated output: %s", value.Message.Content)
				}
			}
			if adapter.starts.Load() != 1 || adapter.resumes.Load() != 1 {
				t.Fatalf("starts=%d resumes=%d", adapter.starts.Load(), adapter.resumes.Load())
			}
		})
	}
}
func TestAcceptedRunSurvivesWithoutAnyRunner(t *testing.T) {
	store, err := repo.Open(repo.Config{DSN: filepath.Join(t.TempDir(), "run.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	_, _ = store.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"})
	adapter := &replayAdapter{lost: make(chan struct{})}
	svc := service.NewHarnessRunService(store, map[string]harness.Adapter{"test": adapter}, NewHarnessRunner(store, map[string]harness.Adapter{"test": adapter}, nil))
	request := contract.HarnessRunRequest{ConversationID: "conv", IdempotencyKey: "once", EffectKey: "work", Target: "test", Text: "go", Timeout: time.Minute}
	first, err := svc.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.Submit(ctx, request)
	if err != nil || again.ID != first.ID || !again.DeadlineAt.Equal(first.DeadlineAt) {
		t.Fatalf("retry=%+v %v", again, err)
	}
	if adapter.starts.Load() != 0 {
		t.Fatal("API executed the Harness")
	}
	pending, err := store.ListRunnableHarnessRuns(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v %v", pending, err)
	}
}
