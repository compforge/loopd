package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/testutil"
)

type fakeHarnessAdapter struct{ release <-chan struct{} }

func (a *fakeHarnessAdapter) Prompt(ctx context.Context, r harness.Request) (harness.Call, error) {
	events := make(chan harness.Event)
	close(events)
	return fakeHarnessCall{id: r.CallID, release: a.release, events: events}, nil
}

type fakeHarnessCall struct {
	id      string
	release <-chan struct{}
	events  chan harness.Event
}

func (c fakeHarnessCall) ID() string                   { return c.id }
func (c fakeHarnessCall) Events() <-chan harness.Event { return c.events }
func (c fakeHarnessCall) Wait(ctx context.Context) (harness.Result, error) {
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return harness.Result{}, ctx.Err()
		}
	}
	return harness.Result{JSON: json.RawMessage(`{"answer":42}`)}, nil
}
func TestHarnessRemoteIdentityAndRestoredResult(t *testing.T) {
	base := httptest.NewServer(http.NotFoundHandler())
	defer base.Close()
	release := make(chan struct{})
	adapter := &fakeHarnessAdapter{release: release}
	client := testutil.WithHarnesses(t, base.Client(), map[string]harness.Adapter{"demo": adapter}, nil)
	rt, _ := New(base.URL, Options{HTTPClient: client})
	defer rt.Close()
	prompt := Prompt{ConversationID: "conv", IdempotencyKey: "key", EffectKey: "plan", Target: "demo", Text: "hello", Timeout: time.Minute}
	call, err := rt.Loop.Harness.Prompt(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	same, err := rt.Loop.Harness.Prompt(context.Background(), prompt)
	if err != nil || same.ID() != call.ID() {
		t.Fatalf("identity: %v %v", same, err)
	}
	changed := prompt
	changed.Text = "different"
	if _, err := rt.Loop.Harness.Prompt(context.Background(), changed); !errors.Is(err, ErrCallConflict) {
		t.Fatalf("conflict=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := call.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait=%v", err)
	}
	_ = rt.Close()
	close(release)
	restored, _ := New(base.URL, Options{HTTPClient: client})
	defer restored.Close()
	wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	result, err := restored.Loop.Harness.Call(call.ID()).Result(wait)
	if err != nil || result.Format != "json" || result.Text() != `{"answer":42}` {
		t.Fatalf("restored result=%+v err=%v", result, err)
	}
}
func TestHarnessExplicitCancellation(t *testing.T) {
	base := httptest.NewServer(http.NotFoundHandler())
	defer base.Close()
	client := testutil.WithHarnesses(t, base.Client(), map[string]harness.Adapter{"demo": &fakeHarnessAdapter{release: make(chan struct{})}}, nil)
	rt, _ := New(base.URL, Options{HTTPClient: client})
	defer rt.Close()
	call, err := rt.Loop.Harness.Prompt(context.Background(), Prompt{ConversationID: "conv", IdempotencyKey: "cancel", EffectKey: "plan", Target: "demo", Text: "hello", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := call.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := call.Wait(ctx)
	if err == nil || value.Phase != contract.CallCancelled {
		t.Fatalf("cancel=%+v %v", value, err)
	}
}
