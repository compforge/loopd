// Package testutil hosts real Harness services alongside Operator HTTP fixtures.
package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/redis/go-redis/v9"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/server/internal/component"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

type transport struct {
	base   http.RoundTripper
	target *url.URL
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasPrefix(r.URL.Path, "/v1/harness/") {
		copy := r.Clone(r.Context())
		u := *r.URL
		copy.URL = &u
		copy.URL.Scheme = t.target.Scheme
		copy.URL.Host = t.target.Host
		return t.base.RoundTrip(copy)
	}
	return t.base.RoundTrip(r)
}
func WithHarnesses(t *testing.T, client *http.Client, adapters map[string]harness.Adapter, publish func(contract.Message)) *http.Client {
	t.Helper()
	store, err := repo.Open(repo.Config{DSN: filepath.Join(t.TempDir(), "harness.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	bridge := agentuerunner.NewRedisEventBridge(redisClient, agentuerunner.BridgeOptions{ReadBlock: time.Millisecond})
	output := service.NewMessageService(store, bridge, nil)
	runner := component.NewHarnessRunner(store, adapters, nil)
	runner.Publish = output.PublishCommitted
	runner.ScanInterval = 5 * time.Millisecond
	svc := service.NewHarnessRunService(store, adapters, runner.Wake)
	svc.Messages = output
	svc.Listen = func(ctx context.Context, id string, deliver func(ui.Event) error) error {
		return component.NewMessageListener(bridge, store, id, nil).Run(ctx, deliver)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); runner.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fail := func(err error) {
			status := 500
			if errors.Is(err, repo.ErrConflict) {
				status = 409
			}
			if errors.Is(err, service.ErrInvalid) {
				status = 400
			}
			if errors.Is(err, repo.ErrNotFound) {
				status = 404
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": err.Error()}})
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/harness/runs" {
			var input contract.HarnessRunRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				fail(err)
				return
			}
			mu.Lock()
			if _, err := store.GetConversation(r.Context(), input.ConversationID); errors.Is(err, repo.ErrNotFound) {
				_, err = store.CreateConversation(r.Context(), model.Conversation{ID: input.ConversationID, ActorKind: contract.ActorKindOperator, ActorKey: "fixture"})
				if err != nil {
					mu.Unlock()
					fail(err)
					return
				}
			}
			mu.Unlock()
			run, err := svc.Submit(r.Context(), input)
			if err != nil {
				fail(err)
				return
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(run)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/harness/runs/")
		if strings.HasSuffix(id, "/stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			err := svc.Stream(r.Context(), strings.TrimSuffix(id, "/stream"), func(event ui.Event) error {
				raw, err := event.Marshal()
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
					return err
				}
				w.(http.Flusher).Flush()
				return nil
			})
			if err != nil && r.Context().Err() == nil {
				t.Logf("Harness fixture stream: %v", err)
			}
			return
		}
		if strings.HasSuffix(id, "/cancel") {
			err := svc.Cancel(r.Context(), strings.TrimSuffix(id, "/cancel"))
			if err != nil {
				fail(err)
				return
			}
			w.WriteHeader(202)
			return
		}
		observation, err := svc.Observe(r.Context(), id)
		if err != nil {
			fail(err)
			return
		}
		if publish != nil {
			publish(observation.Message)
		}
		_ = json.NewEncoder(w).Encode(observation.Call)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	copy := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = transport{base: base, target: target}
	return &copy
}
