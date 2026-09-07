package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestHarnessPromptReturnsCapacityWithoutAutomaticRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"harness_capacity_exceeded","message":"Harness execution capacity exhausted"}}`))
	}))
	defer server.Close()
	rt, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	call, err := rt.Loop.Harness.Prompt(context.Background(), Prompt{ConversationID: "conv", IdempotencyKey: "key", Target: "test", EffectKey: "work", Text: "go"})
	if call != nil || !IsHarnessCapacityExceeded(err) || !IsRetryable(err) || requests.Load() != 1 {
		t.Fatalf("call=%v err=%v requests=%d", call, err, requests.Load())
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.StatusCode != 429 {
		t.Fatalf("typed error lost: %v", err)
	}
	if IsHarnessCapacityExceeded(&Error{StatusCode: 429, Type: "other_limit"}) {
		t.Fatal("unrelated 429 classified as Harness capacity")
	}
}
