package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
)

func TestHarnessUsesBoundMessageAndLeavesEndToCaller(t *testing.T) {
	var speaks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/conversations/workspace/speak" {
			speaks.Add(1)
			var in contract.SpeakRequest
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(contract.Message{ID: in.Key, ConversationID: "workspace", Kind: in.Actor.Kind, Key: in.Actor.Key, Status: contract.MessageStatusStreaming, Revision: 1, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	adapter := &fakeHarnessAdapter{}
	runtime, err := New(server.URL, Options{HTTPClient: server.Client(), Harnesses: map[string]harness.Adapter{"demo": adapter}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	ctx := context.Background()
	author := contract.ActorRef{Kind: "operator/example/manager", Key: "run"}
	output, err := runtime.Loop.Conv.Speak(ctx, "workspace", contract.SpeakRequest{Key: "step", Actor: author, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	prompt := Prompt{ConversationID: "workspace", IdempotencyKey: "step", EffectKey: "plan", Target: "demo", Text: "hello", Actor: &author, Output: output}
	call, err := runtime.Loop.Harness.Prompt(ctx, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if speaks.Load() != 1 || output.Value().Status != contract.MessageStatusStreaming || !strings.Contains(string(output.Value().Content), "hello") {
		t.Fatalf("bound output not streamed or ended by Harness: speaks=%d, output=%+v", speaks.Load(), output.Value())
	}
	if err := output.End(ctx); err != nil {
		t.Fatal(err)
	}
	// Neither changing revision nor ending the message changes Call identity.
	again, err := runtime.Loop.Harness.Prompt(ctx, prompt)
	if err != nil || again != call || adapter.starts != 1 {
		t.Fatalf("Call not reused: same=%v starts=%d err=%v", again == call, adapter.starts, err)
	}
	other, err := runtime.Loop.Conv.Speak(ctx, "workspace", contract.SpeakRequest{Key: "other", Actor: author, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	changed := prompt
	changed.Output = other
	if _, err := runtime.Loop.Harness.Prompt(ctx, changed); !errors.Is(err, ErrCallConflict) {
		t.Fatalf("changed output binding: %v", err)
	}
	changed = prompt
	changed.ConversationID = "other-conv"
	if _, err := runtime.Loop.Harness.Prompt(ctx, changed); err == nil {
		t.Fatal("accepted output from another conversation")
	}
	changed = prompt
	changed.IdempotencyKey = "new-call"
	if _, err := runtime.Loop.Harness.Prompt(ctx, changed); err == nil {
		t.Fatal("started a new Call with an ended message")
	}
	if adapter.starts != 1 {
		t.Fatalf("invalid bindings started Harnesses: %d", adapter.starts)
	}
}
