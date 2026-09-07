package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// +case=`Short requests and streams reuse separate connections, and idle cleanup releases both pools.`
func TestPoolReuseAndCleanup(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client, err := New(Config{RequestTimeout: time.Second, MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	request := func(do DoFunc) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := do.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || string(body) != "ok" {
			t.Fatalf("body=%q err=%v", body, err)
		}
	}
	for i := 0; i < 2; i++ {
		request(client.DoShort)
		request(client.DoStream)
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("connections=%d, want one reused connection per pool", got)
	}
	client.CloseIdleConnections()
	request(client.DoShort)
	request(client.DoStream)
	if got := connections.Load(); got != 4 {
		t.Fatalf("connections after cleanup=%d, want 4", got)
	}
}

// +case=`Short body reads time out, while streams survive that timeout and idle cleanup until their context is cancelled.`
func TestBodyTimeoutAndStreamCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	timeout := 150 * time.Millisecond
	client, err := New(Config{RequestTimeout: timeout, MaxConnections: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.DoShort(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short body error=%v, want deadline exceeded", err)
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	resp, err = client.DoStream(req.Clone(streamCtx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		done <- err
	}()
	client.CloseIdleConnections()
	select {
	case err := <-done:
		t.Fatalf("stream ended before context cancellation: %v", err)
	case <-time.After(2 * timeout):
	}
	cancelStream()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stream body error=%v, want cancelled", err)
		}
	case <-ctx.Done():
		t.Fatal("stream body did not stop after cancellation")
	}
}
