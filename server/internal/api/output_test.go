package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/redis/go-redis/v9"
)

func TestOutputHTTPIdentityAndWriteBoundaries(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "outputs.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	bridge := agentuerunner.NewRedisEventBridge(client, agentuerunner.BridgeOptions{ReadBlock: time.Millisecond})
	chat := service.NewChatService(store, delivery.New(bridge, store, nil), nil, nil)
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil), chat, service.NewChatContextService(store, nil), nil)
	engine := route.NewEngine(config.NewOptions(nil))
	api.Register(engine)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	main, err := chat.Create(ctx, "root", "alice", loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/tasks/" + main.TaskID
	body := `{"key":"work/0","actor":{"kind":"harness","key":"agent"}}`
	result := performJSON(t, engine, "POST", path+"/outputs", body)
	if result.StatusCode() != 200 {
		t.Fatalf("create=%d %s", result.StatusCode(), result.Body())
	}
	var output loopd.Message
	if err := json.Unmarshal(result.Body(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Purpose != "output" || output.TaskID != main.TaskID || output.ConversationID == "root" {
		t.Fatalf("output=%+v", output)
	}
	again := performJSON(t, engine, "POST", path+"/outputs", body)
	var repeated loopd.Message
	if err := json.Unmarshal(again.Body(), &repeated); err != nil || repeated.ID != output.ID {
		t.Fatalf("retry=%s %v", again.Body(), err)
	}
	event := `{"event":{"op":"set","seq":2,"block":{"id":"text","type":"text","content":"work"}}}`
	accepted := performJSON(t, engine, "POST", path+"/messages/"+output.ID+"/events", event)
	if accepted.StatusCode() != 202 {
		t.Fatalf("emit=%d %s", accepted.StatusCode(), accepted.Body())
	}
	wrong := performJSON(t, engine, "POST", "/v1/tasks/other/messages/"+output.ID+"/events", event)
	if wrong.StatusCode() != 404 {
		t.Fatalf("wrong task=%d %s", wrong.StatusCode(), wrong.Body())
	}
	reserved := performJSON(t, engine, "POST", path+"/messages/"+output.ID+"/events", `{"event":{"op":"set","seq":3,"block":{"id":"human","type":"confirm"}}}`)
	if reserved.StatusCode() != 400 {
		t.Fatalf("reserved Human=%d %s", reserved.StatusCode(), reserved.Body())
	}
	done := performJSON(t, engine, "POST", path+"/complete", `{}`)
	if done.StatusCode() != 204 {
		t.Fatalf("complete=%d %s", done.StatusCode(), done.Body())
	}
	late := performJSON(t, engine, "POST", path+"/messages/"+output.ID+"/events", event)
	if late.StatusCode() != 409 {
		t.Fatalf("late=%d %s", late.StatusCode(), late.Body())
	}
}
