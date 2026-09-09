package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

func TestHarnessStreamUsesServerSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/harness/runs/run/stream" {
			t.Errorf("unexpected SQL polling path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"op\":\"start\",\"seq\":4,\"model\":{\"version\":\"1.1\",\"biz\":\"chat\",\"meta\":{},\"blocks\":[]}}\n\n")
		fmt.Fprint(w, "data: {\"op\":\"append\",\"seq\":5,\"mask\":\"block.content\",\"block\":{\"id\":\"text\",\"type\":\"text\",\"content\":\"hi\"}}\n\n")
		fmt.Fprint(w, "data: {\"op\":\"end\",\"seq\":6}\n\n")
	}))
	defer server.Close()
	rt, _ := New(server.URL, Options{})
	defer rt.Close()
	events, failures := rt.Loop.Harness.Call("run").Stream(context.Background())
	var ops []string
	for event := range events {
		ops = append(ops, string(event.Op))
	}
	for err := range failures {
		t.Fatal(err)
	}
	if fmt.Sprint(ops) != "[start append end]" {
		t.Fatalf("ops=%v", ops)
	}
}

// +case=`Wait reads metadata until terminal, fetches only the result block, and never opens the output stream or snapshot.`
func TestHarnessWaitPollsStateWithoutReadingOutput(t *testing.T) {
	for _, phase := range []contract.CallPhase{contract.CallSucceeded, contract.CallFailed, contract.CallCancelled} {
		t.Run(string(phase), func(t *testing.T) {
			var reads, results atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/harness/runs/run":
					value := contract.HarnessCall{ID: "run", ConversationID: "conv", MessageID: "output", Phase: contract.CallRunning}
					if reads.Add(1) >= 2 {
						value.Phase = phase
						value.Error = "execution stopped"
					}
					_ = json.NewEncoder(w).Encode(value)
				case "/v1/conversations/conv/messages/output/blocks/result":
					results.Add(1)
					fmt.Fprint(w, `{"revision":2,"block":{"id":"result","type":"result","format":"text","content":"answer"}}`)
				default:
					t.Errorf("Wait fetched execution content: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			rt, _ := New(server.URL, Options{})
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			value, err := rt.Loop.Harness.Call("run").Wait(ctx)
			if value.Phase != phase || (err != nil) != (phase != contract.CallSucceeded) || reads.Load() != 2 {
				t.Fatalf("value=%+v err=%v reads=%d", value, err, reads.Load())
			}
			expected := int32(0)
			if phase == contract.CallSucceeded {
				expected = 1
				if value.Result == nil || value.Result.Text() != "answer" {
					t.Fatalf("result=%+v", value.Result)
				}
			}
			if results.Load() != expected {
				t.Fatalf("result reads=%d", results.Load())
			}
		})
	}
}
func TestHarnessResultAlreadyCompletedSkipsStream(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/harness/runs/run" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		reads.Add(1)
		result := contract.TextResult("answer")
		_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallSucceeded, Result: &result})
	}))
	defer server.Close()
	rt, _ := New(server.URL, Options{})
	defer rt.Close()
	result, err := rt.Loop.Harness.Call("run").Result(context.Background())
	if err != nil || result == nil || result.Text() != "answer" || reads.Load() != 1 {
		t.Fatalf("result=%+v err=%v reads=%d", result, err, reads.Load())
	}
}
func TestHarnessWaitStopsObservation(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		t.Run(fmt.Sprint(forbidden), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/harness/runs/run" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if forbidden {
					w.WriteHeader(http.StatusForbidden)
				} else {
					_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallRunning})
					cancel()
				}
			}))
			defer server.Close()
			rt, _ := New(server.URL, Options{})
			defer rt.Close()
			_, err := rt.Loop.Harness.Call("run").Wait(ctx)
			if forbidden {
				var value *Error
				if !errors.As(err, &value) || value.StatusCode != http.StatusForbidden || value.Retryable {
					t.Fatalf("error=%v", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func TestHarnessWaitBoundsConsecutiveReadFailures(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/harness/runs/run" {
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				n := reads.Add(1)
				// A successful pending read resets the consecutive failure budget.
				if recover && n == 3 {
					_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallRunning})
					return
				}
				if recover && n == 6 {
					result := contract.TextResult("answer")
					_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallSucceeded, Result: &result})
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			rt, _ := New(server.URL, Options{})
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			value, err := rt.Loop.Harness.Call("run").Wait(ctx)
			if recover {
				if err != nil || value.Phase != contract.CallSucceeded || reads.Load() != 6 {
					t.Fatalf("value=%+v err=%v reads=%d", value, err, reads.Load())
				}
			} else if !IsRetryable(err) || reads.Load() != 3 {
				t.Fatalf("error=%v reads=%d", err, reads.Load())
			}
		})
	}
}
