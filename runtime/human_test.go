package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
)

func TestHumanHandleWaitAndRequestContract(t *testing.T) {
	var mu sync.Mutex
	state := loopd.HumanPending
	var request loopd.HumanRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(loopd.HumanResult{Message: loopd.Message{ID: "question"}, Status: state, Deadline: time.Now().Add(time.Minute)})
	}))
	defer server.Close()
	runtime, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	h, err := runtime.Loop.Human.Ask(context.Background(), AskRequest{ConversationID: "conv", Actor: loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, Target: loopd.ActorRef{Kind: loopd.RoleUser, Key: "alice"}, EffectKey: "scope", Title: "Scope", Prompt: "Choose", Timeout: time.Minute, AllowOther: true})
	mu.Lock()
	captured := request
	mu.Unlock()
	if err != nil || h.ID() != "question" || captured.Timeout != time.Minute || !captured.AllowOther {
		t.Fatalf("handle=%+v request=%+v %v", h, captured, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel wait=%v", err)
	}
	// Context cancellation only stopped observation. The server still owns state.
	result, err := h.Get(context.Background())
	if err != nil || result.Status != loopd.HumanPending {
		t.Fatalf("cancel changed request=%+v %v", result, err)
	}
	for _, status := range []loopd.HumanStatus{loopd.HumanDismissed, loopd.HumanTimeout} {
		mu.Lock()
		state = status
		mu.Unlock()
		result, err := h.Wait(context.Background())
		if err != nil || result.Status != status {
			t.Fatalf("normal outcome=%+v %v", result, err)
		}
	}
}
