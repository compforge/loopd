package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/compforge/loopd/pkg/contract"
	loopruntime "github.com/compforge/loopd/runtime"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

// +case=`A succeeded Run with a multi-MiB result remains readable after a lost stream through Call.Get, Call.Result and Message reads; storage framing is invisible.`
func TestLargeHarnessResultRuntimeReads(t *testing.T) {
	store, err := repo.Open(repo.Config{DSN: filepath.Join(t.TempDir(), "result.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil, service.NewMessageService(store, nil, nil), nil, nil)
	s.HarnessRuns = service.NewHarnessRunService(store, nil, nil)
	engine := route.NewEngine(config.NewOptions(nil))
	s.Register(engine)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stream") {
			// Exercise recovery from DB, not an in-memory result or live event.
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
			return
		}
		response := ut.PerformRequest(engine, r.Method, r.URL.RequestURI(), nil).Result()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode())
		_, _ = w.Write(response.Body())
	}))
	defer httpServer.Close()
	rt, err := loopruntime.New(httpServer.URL, loopruntime.Options{HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	for _, size := range []int{2 << 20, 17 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			run, err := store.CreateHarnessRun(ctx, contract.HarnessRunRequest{ConversationID: "conv", IdempotencyKey: fmt.Sprint(size), EffectKey: "work", Target: "test", Text: "large result", Timeout: time.Minute, Actor: &contract.ActorRef{Kind: "harness", Key: "test"}, Meta: map[string]any{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			token, err := store.Lock(ctx, repo.HarnessResource(run.ID), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Repeat("x", size)
			result := contract.TextResult(want)
			if _, err := store.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, ""); err != nil {
				t.Fatal(err)
			}
			call := rt.Loop.Harness.Call(run.ID)
			got, err := call.Get(ctx)
			if err != nil || got.Phase != contract.CallSucceeded || got.Result == nil || got.Result.Text() != want {
				t.Fatalf("Call.Get cannot read accepted result: phase=%s err=%v", got.Phase, err)
			}
			final, err := call.Result(ctx)
			if err != nil || final == nil || final.Text() != want {
				t.Fatalf("Call.Result: %v", err)
			}
			message := rt.Loop.Conv.Read("conv", run.MessageID)
			snapshot, err := message.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := contract.ExtractResult(snapshot.Content)
			if err != nil || restored == nil || restored.Text() != want {
				t.Fatalf("snapshot result: %v", err)
			}
			count := 0
			err = message.Blocks(ctx, func(block loopruntime.BlockSnapshot) error {
				count++
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(block.Block, &fields); err != nil {
					return err
				}
				if string(fields["type"]) == `"frame"` || fields["ref"] != nil {
					return fmt.Errorf("physical content escaped storage")
				}
				return nil
			})
			if err != nil || count != 1 {
				t.Fatalf("Blocks count=%d err=%v", count, err)
			}
		})
	}
}
