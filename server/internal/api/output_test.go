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
	"github.com/compforge/loopd/pkg/contract"
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
	chat := service.NewChatService(store, nil, nil)
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, bridge, nil), chat, nil)
	engine := route.NewEngine(config.NewOptions(nil))
	api.Register(engine)
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	_, err = chat.Create(ctx, "root", "alice", contract.ActorRef{Kind: contract.ActorKindOperator, Key: "router"}, []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`))
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
	var spoken contract.Message
	if err := json.Unmarshal(speech.Body(), &spoken); err != nil {
		t.Fatal(err)
	}
	if spoken.TaskID != "" || spoken.ConversationID != "root" {
		t.Fatalf("speech=%+v", spoken)
	}
	retry := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"stream":true,"key":"progress/1","actor":{"kind":"operator","key":"router"},"target":{"kind":"user","key":"alice"}}`)
	var same contract.Message
	if err := json.Unmarshal(retry.Body(), &same); err != nil || same.ID != spoken.ID {
		t.Fatalf("speech retry=%s %v", retry.Body(), err)
	}
	changed := performJSON(t, engine, "POST", "/v1/conversations/root/speak",
		`{"stream":true,"key":"progress/1","actor":{"kind":"operator","key":"router"},"target":{"kind":"operator","key":"another"}}`)
	if changed.StatusCode() != 409 {
		t.Fatalf("changed recipient=%d", changed.StatusCode())
	}
	speechPath := "/v1/messages/" + spoken.ID + "/events"
	// +case=`引用是 repo 内部格式；API 拒绝外部引用并保留原消息。`
	forged := performJSON(t, engine, "POST", speechPath, `{"event":{"op":"set","seq":2,"block":{"id":"text","ref":"foreign-part"}}}`)
	if forged.StatusCode() != 400 {
		t.Fatalf("reference event=%d %s", forged.StatusCode(), forged.Body())
	}
	forged = performJSON(t, engine, "POST", "/v1/conversations/root/speak", `{"key":"forged","actor":{"kind":"operator","key":"router"},"content":{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"text","ref":"foreign-part"}]}}`)
	if forged.StatusCode() != 400 {
		t.Fatalf("reference snapshot=%d %s", forged.StatusCode(), forged.Body())
	}

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
		Data []contract.Message `json:"data"`
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
	var final contract.Message
	if err := json.Unmarshal(once.Body(), &final); err != nil || once.StatusCode() != 200 || !final.Ended() {
		t.Fatalf("one-shot=%s %v", once.Body(), err)
	}
	removed := performJSON(t, engine, "GET", "/v1/conversations/root/messages/"+spoken.ID+"/context", "")
	if removed.StatusCode() != 404 {
		t.Fatalf("removed context route=%d", removed.StatusCode())
	}
}

// +case=`A complete error report persists AgentUE error and failed status together; retries cannot overwrite or reopen it.`
func TestSpeakTerminalStatus(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "failed.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	api := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil, nil), nil, nil)
	engine := route.NewEngine(config.NewOptions(nil))
	api.Register(engine)
	request := contract.SpeakRequest{
		Key: "failure", Actor: contract.ActorRef{Kind: contract.ActorKindOperator, Key: "router"},
		Status:  contract.MessageStatusFailed,
		Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{"error":{"code":"operator_failed","message":"Please retry."}},"blocks":[]}`),
	}
	speak := func(request contract.SpeakRequest) contract.Message {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response := performJSON(t, engine, "POST", "/v1/conversations/root/speak", string(body))
		var message contract.Message
		if response.StatusCode() != 200 {
			t.Fatalf("speak=%d %s", response.StatusCode(), response.Body())
		}
		if err := json.Unmarshal(response.Body(), &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	first := speak(request)
	if first.Status != contract.MessageStatusFailed || !first.Ended() {
		t.Fatalf("message=%+v", first)
	}
	stored, err := store.GetMessage(ctx, first.ID)
	if err != nil || stored.Status != string(contract.MessageStatusFailed) || !strings.Contains(string(stored.Content), "operator_failed") {
		t.Fatalf("persisted error=%+v err=%v", stored, err)
	}
	request.Status = ""
	request.Content = json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)
	for _, stream := range []bool{false, true} {
		request.Stream = stream
		retry := speak(request)
		if retry.ID != first.ID || retry.Status != first.Status || string(retry.Content) != string(first.Content) {
			t.Fatalf("retry changed error: %+v", retry)
		}
	}
	for _, invalid := range []contract.SpeakRequest{
		{Key: "invalid-stream", Actor: request.Actor, Stream: true, Status: contract.MessageStatusFailed},
		{Key: "invalid-status", Actor: request.Actor, Status: contract.MessageStatusStreaming},
		{Key: "unknown-status", Actor: request.Actor, Status: "unknown"},
	} {
		body, _ := json.Marshal(invalid)
		response := performJSON(t, engine, "POST", "/v1/conversations/root/speak", string(body))
		if response.StatusCode() != 400 {
			t.Fatalf("invalid status=%d %s", response.StatusCode(), response.Body())
		}
	}
	history, err := store.ListMessages(ctx, "root", "", 100)
	if err != nil || len(history) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	request.Key, request.Stream = "completed", false
	if completed := speak(request); completed.Status != contract.MessageStatusCompleted {
		t.Fatalf("default=%+v", completed)
	}
}
