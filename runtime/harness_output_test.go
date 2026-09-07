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

func TestHarnessResultWaitsForStreamAndReadsTerminalStateOnce(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			var streams, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/harness/runs/run/stream":
					streams.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"op\":\"start\",\"seq\":1,\"model\":{\"version\":\"1.1\",\"biz\":\"chat\",\"meta\":{},\"blocks\":[]}}\n\n")
					if complete {
						fmt.Fprint(w, "data: {\"op\":\"end\",\"seq\":2}\n\n")
					}
				case "/v1/harness/runs/run":
					reads.Add(1)
					result := contract.TextResult("answer")
					_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallSucceeded, Result: &result})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			rt, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			result, err := rt.Loop.Harness.Call("run").Result(context.Background())
			if err != nil || result.Text() != "answer" || reads.Load() != 1 {
				t.Fatalf("result=%+v err=%v reads=%d", result, err, reads.Load())
			}
			if streams.Load() != 1 {
				t.Fatalf("streams=%d", streams.Load())
			}
		})
	}
}

func TestHarnessWaitReattachesAfterDisconnect(t *testing.T) {
	for _, terminal := range []contract.CallPhase{contract.CallSucceeded, contract.CallFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			var streams, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/harness/runs/run/stream":
					w.Header().Set("Content-Type", "text/event-stream")
					if streams.Add(1) > 1 {
						fmt.Fprint(w, "data: {\"op\":\"end\",\"seq\":2}\n\n")
					} // First connection closes before End while execution continues.
				case "/v1/harness/runs/run":
					value := contract.HarnessCall{ID: "run", Phase: contract.CallRunning}
					if reads.Add(1) > 1 {
						value.Phase = terminal
						if terminal == contract.CallSucceeded {
							result := contract.TextResult("answer")
							value.Result = &result
						} else {
							value.Error = "execution failed"
						}
					}
					_ = json.NewEncoder(w).Encode(value)
				default:
					t.Errorf("unexpected request (must not restart/cancel): %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			rt, _ := New(server.URL, Options{})
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			value, err := rt.Loop.Harness.Call("run").Wait(ctx)
			if value.Phase != terminal || (err != nil) != (terminal == contract.CallFailed) || IsRetryable(err) {
				t.Fatalf("value=%+v error=%v", value, err)
			}
			if streams.Load() != 2 || reads.Load() != 2 {
				t.Fatalf("streams=%d reads=%d", streams.Load(), reads.Load())
			}
		})
	}
}

func TestHarnessWaitStopsObservation(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		t.Run(fmt.Sprint(forbidden), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var streams atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/harness/runs/run/stream":
					streams.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
				case "/v1/harness/runs/run":
					if forbidden {
						w.WriteHeader(http.StatusForbidden)
					} else {
						_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallRunning})
						cancel()
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			rt, _ := New(server.URL, Options{})
			defer rt.Close()
			_, err := rt.Loop.Harness.Call("run").Wait(ctx)
			if forbidden {
				var value *Error
				if !errors.As(err, &value) || value.StatusCode != http.StatusForbidden || value.Retryable {
					t.Fatalf("expected non-retryable forbidden error: %v", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation: %v", err)
			}
			if streams.Load() != 1 {
				t.Fatalf("error=%v streams=%d", err, streams.Load())
			}
		})
	}
}

func TestHarnessWaitReturnsAfterRepeatedDisconnects(t *testing.T) {
	var streams atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/harness/runs/run/stream":
			streams.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/v1/harness/runs/run":
			_ = json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", Phase: contract.CallRunning})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	rt, _ := New(server.URL, Options{})
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	value, err := rt.Loop.Harness.Call("run").Wait(ctx)
	if !IsRetryable(err) || value.Phase != contract.CallRunning || streams.Load() != 3 {
		t.Fatalf("value=%+v error=%v streams=%d", value, err, streams.Load())
	}
}
