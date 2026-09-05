package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil), chat, nil)
	engine := route.NewEngine(config.NewOptions(nil))
	api.Register(engine)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	_, err = chat.Create(ctx, "root", "alice", loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	event := `{"event":{"op":"set","seq":2,"block":{"id":"text","type":"text","content":"work"}}}`
	// +case=`An actor can speak and stream independently of UI delivery, without a task ID.`
	speech := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"stream":true,"key":"progress/1","actor":{"kind":"operator","key":"router"},"target":{"kind":"user","key":"alice"}}`)
	if speech.StatusCode() != 200 {
		t.Fatalf("speak=%d %s", speech.StatusCode(), speech.Body())
	}
	var spoken loopd.Message
	if err := json.Unmarshal(speech.Body(), &spoken); err != nil {
		t.Fatal(err)
	}
	if spoken.TaskID != "" || spoken.ConversationID != "root" {
		t.Fatalf("speech=%+v", spoken)
	}
	retry := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"stream":true,"key":"progress/1","actor":{"kind":"operator","key":"router"},"target":{"kind":"user","key":"alice"}}`)
	var same loopd.Message
	if err := json.Unmarshal(retry.Body(), &same); err != nil || same.ID != spoken.ID {
		t.Fatalf("speech retry=%s %v", retry.Body(), err)
	}
	changed := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"stream":true,"key":"progress/1","actor":{"kind":"operator","key":"router"},"target":{"kind":"operator","key":"another"}}`)
	if changed.StatusCode() != 409 {
		t.Fatalf("changed recipient=%d", changed.StatusCode())
	}
	speechPath := "/v1/messages/" + spoken.ID + "/events"
	for i := 0; i < 2; i++ {
		result := performJSON(t, engine, "POST", speechPath, event)
		if result.StatusCode() != 202 {
			t.Fatalf("stream/retry=%d %s", result.StatusCode(), result.Body())
		}
	}
	projected, err := store.GetMessage(ctx, spoken.ID)
	if err != nil || projected.Revision != 2 || !strings.Contains(string(projected.Content), "work") {
		t.Fatalf("snapshot=%+v err=%v", projected, err)
	}
	ended := performJSON(t, engine, "POST", speechPath, `{"event":{"op":"end","seq":3}}`)
	if ended.StatusCode() != 202 {
		t.Fatalf("end=%d %s", ended.StatusCode(), ended.Body())
	}
	view := performJSON(t, engine, "GET", "/v1/conversations/root/messages", "")
	var history struct {
		Data []loopd.Message `json:"data"`
	}
	if err := json.Unmarshal(view.Body(), &history); err != nil {
		t.Fatal(err)
	}
	if view.StatusCode() != 200 || len(history.Data) != 2 || history.Data[1].ID != spoken.ID {
		t.Fatalf("history=%d %s", view.StatusCode(), view.Body())
	}
	complete := performJSON(t, engine, "POST", "/v1/deliveries/old/complete", "{}")
	if complete.StatusCode() != 404 {
		t.Fatalf("removed Complete route=%d", complete.StatusCode())
	}
	once := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"key":"once","actor":{"kind":"operator","key":"router"},"content":{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"answer","type":"text","content":"done"}]}}`)
	var final loopd.Message
	if err := json.Unmarshal(once.Body(), &final); err != nil || once.StatusCode() != 200 || !final.Ended() {
		t.Fatalf("one-shot=%s %v", once.Body(), err)
	}
	removed := performJSON(t, engine, "GET", "/v1/conversations/root/messages/"+spoken.ID+"/context", "")
	if removed.StatusCode() != 404 {
		t.Fatalf("removed context route=%d", removed.StatusCode())
	}
}
