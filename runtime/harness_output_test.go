package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
			if complete {
				if err != nil || result.Text() != "answer" || reads.Load() != 1 {
					t.Fatalf("result=%+v err=%v reads=%d", result, err, reads.Load())
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) || reads.Load() != 0 {
				t.Fatalf("truncated stream: %v reads=%d", err, reads.Load())
			}
			if streams.Load() != 1 {
				t.Fatalf("streams=%d", streams.Load())
			}
		})
	}
}
