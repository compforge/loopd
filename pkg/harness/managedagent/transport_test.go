package managedagent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/compforge/loopd/pkg/harness"
)

// +case=`Concurrent SSE connections filling their pool still allow history reads through the official SDK.`
func TestConcurrentStreamsLeaveCapacityForHistory(t *testing.T) {
	var streams, histories atomic.Int32
	allStreams := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stream") {
			if streams.Add(1) == 2 {
				close(allStreams)
			}
			select {
			case <-allStreams:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		histories.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s,%s,%s],"has_more":false}`, inputEvent, answerEvent, endEvent)
	}))
	defer server.Close()
	config := testConfig(server.URL)
	config.MaxConnections = 2
	config.HTTPTimeout = 300 * time.Millisecond
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	calls := make([]harness.Call, 0, 2)
	for i := 0; i < 2; i++ {
		request := testRequest()
		request.CallID = fmt.Sprint(i)
		request.ExecutionRef = fmt.Sprint("session-", i)
		call, err := adapter.Resume(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	for _, call := range calls {
		for range call.Events() {
		}
		result, err := call.Wait(ctx)
		if err != nil || result.Text != "hello world" {
			t.Errorf("run %s: result=%+v err=%v", call.ID(), result, err)
		}
	}
	t.Logf("SSE connections=%d history requests reaching server=%d", streams.Load(), histories.Load())
	if histories.Load() != 2 {
		t.Fatal("stream connections starved history reads")
	}
}
