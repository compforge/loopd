package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

func TestActorsOnlyListsLiveTargets(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := New(
		service.NewActorService(store, nil),
		service.NewConversationService(store, nil),
		service.NewMessageService(store, nil),
		service.NewChatService(store, nopTaskClient{}, completedChatRunner{}, nil),
		service.NewTaskService(store, nil),
		nil,
	)
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)

	registered := performJSON(t, engine, "PUT", "/v1/operators/router", `{
		"display_name":"Router",
		"description":"Routes requests",
		"lease_seconds":30
	}`)
	if registered.StatusCode() != 200 {
		t.Fatalf("register actor status=%d body=%s", registered.StatusCode(), registered.Body())
	}
	if _, err := store.RegisterHarness(context.Background(), model.Harness{
		ID:  "01991f3d-1115-7000-8000-000000000000",
		Key: "expired", DisplayName: "Expired", ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	response := ut.PerformRequest(engine, "GET", "/v1/actors", nil).Result()
	if response.StatusCode() != 200 {
		t.Fatalf("actors status=%d body=%s", response.StatusCode(), response.Body())
	}
	var actors page[loopd.Actor]
	if err := json.Unmarshal(response.Body(), &actors); err != nil {
		t.Fatal(err)
	}
	if len(actors.Data) != 1 || actors.Data[0].Kind != loopd.RoleOperator || actors.Data[0].Key != "router" {
		t.Fatalf("actors = %#v", actors.Data)
	}
}
