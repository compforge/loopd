package longhorizon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	lr "github.com/compforge/loopd/runtime"
	"github.com/compforge/loopd/server/testutil"
)

type streamedRole struct {
	events chan harness.Event
	err    error
	starts int
	id     string
}

func (role *streamedRole) Prompt(_ context.Context, request harness.Request) (harness.Call, error) {
	role.starts++
	role.id = request.CallID
	return role, nil
}
func (role *streamedRole) ID() string                   { return role.id }
func (role *streamedRole) Events() <-chan harness.Event { return role.events }
func (role *streamedRole) Wait(context.Context) (harness.Result, error) {
	return harness.Result{Text: "Final result"}, role.err
}

func TestRoleStreamsAndCheckpointsOneMessage(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "completed"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			role := &streamedRole{events: make(chan harness.Event, 2)}
			if failed {
				role.err = errors.New("execution failed")
			}
			for _, block := range []map[string]any{
				{"id": "answer", "type": "text", "content": "Partial result"},
				{"id": "tool", "type": "tool", "name": "Read", "status": "completed"},
			} {
				data, err := (ui.Event{Op: ui.OpSet, Block: block}).Marshal()
				if err != nil {
					t.Fatal(err)
				}
				role.events <- harness.Event{Data: data}
			}
			f.harnessClient = testutil.WithHarnesses(t, f.server.Client(), map[string]harness.Adapter{"executor": role}, f.publishHarness)
			runtime, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.harnessClient})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			closed := false
			t.Cleanup(func() {
				if !closed {
					close(role.events)
				}
			})
			f.c.Loop = runtime.Loop
			ctx := context.Background()
			run := f.run()
			_, id, _, done, err := f.c.invoke(ctx, run, 1, ActorExecutor, "executor", "execute", time.Minute)
			if err != nil || done || id == "" {
				t.Fatalf("start: id=%q done=%v err=%v", id, done, err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				_, _, _, _, err = f.c.invoke(ctx, run, 1, ActorExecutor, "executor", "execute", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				m := f.messages[id]
				count := len(f.outputs)
				f.mu.Unlock()
				if count != 1 || m.Status != contract.MessageStatusStreaming {
					t.Fatalf("unexpected streaming output: count=%d message=%+v", count, m)
				}
				if _, err := reportFrom(m); err == nil {
					t.Fatal("partial output was accepted as a recovery checkpoint")
				}
				if m.Revision >= 3 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("streaming output did not arrive")
				}
				time.Sleep(time.Millisecond)
			}
			close(role.events)
			closed = true
			for !done {
				_, restored, _, terminal, err := f.c.invoke(ctx, run, 1, ActorExecutor, "executor", "execute", time.Minute)
				if err != nil || restored != id {
					t.Fatalf("finish: id=%q err=%v", restored, err)
				}
				done = terminal
				if time.Now().After(deadline) {
					t.Fatal("role did not finish")
				}
				time.Sleep(time.Millisecond)
			}
			f.mu.Lock()
			m := f.messages[id]
			count := len(f.outputs)
			f.mu.Unlock()
			if count != 1 || m.Status != contract.MessageStatus(name) {
				t.Fatalf("final output: count=%d message=%+v", count, m)
			}
			result, err := f.c.readReport(ctx, "workspace", id)
			if err != nil || (!failed && result.Text != "Final result") || (result.Error != "") != failed {
				t.Fatalf("report=%+v err=%v", result, err)
			}
			var content struct {
				Blocks []map[string]any `json:"blocks"`
			}
			if err := json.Unmarshal(m.Content, &content); err != nil {
				t.Fatal(err)
			}
			if content.Blocks[0]["content"] != "Partial result" || content.Blocks[1]["id"] != "tool" || (!failed && len(content.Blocks) != 3) || (failed && len(content.Blocks) != 2) {
				t.Fatalf("answer duplicated or tool lost: %+v", content.Blocks)
			}
			if role.starts != 1 {
				t.Fatalf("role started %d times", role.starts)
			}
			// Drop in-memory Calls: the sealed message remains the recovery source.
			fresh, err := lr.New(f.server.URL, lr.Options{HTTPClient: f.harnessClient})
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			f.c.Loop = fresh.Loop
			if _, restored, _, done, err := f.c.invoke(ctx, run, 1, ActorExecutor, "executor", "execute", time.Minute); err != nil || !done || restored != id {
				t.Fatalf("restart: id=%q done=%v err=%v", restored, done, err)
			}
		})
	}
}
