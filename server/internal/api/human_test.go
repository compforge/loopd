package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

func TestHumanHTTPFlowAndTrustedResponder(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "human.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	convs := service.NewConversationService(store, nil)
	chat := service.NewChatService(store, completedChatRunner{}, nil, nil)
	tasks := service.NewContextService(store, nil)
	server := New(service.NewActorService(store, nil), convs, service.NewMessageService(store, nil), chat, tasks, nil)
	server.Human = service.NewHumanService(store, nil)
	actor := "alice"
	server.HumanIdentity = func(context.Context, *hertzapp.RequestContext) (string, error) { return actor, nil }
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)
	response := performJSON(t, engine, "POST", "/v1/conversations", `{"name":"Human"}`)
	var conv loopd.Conversation
	if err := json.Unmarshal(response.Body(), &conv); err != nil {
		t.Fatal(err)
	}
	taskID, _ := performChat(t, server, conv.ID, `{"user_key":"forged","target":{"kind":"operator","key":"router"},"content":{"version":"1.0","biz":"chat","meta":{},"blocks":[]}}`)
	task, err := store.GetDeliveryInput(ctx, taskID)
	if err != nil || task.ActorKey != "alice" {
		t.Fatalf("trusted principal=%+v %v", task, err)
	}
	request := loopd.HumanRequest{ConversationID: conv.ID, Actor: loopd.ActorRef{Kind: loopd.RoleOperator, Key: "router"}, Target: loopd.ActorRef{Kind: loopd.RoleUser, Key: "alice"}, ReplyToID: task.ID, TaskID: taskID, Type: "ask", EffectKey: "scope", Title: "Scope", Prompt: "Choose", Timeout: time.Minute, AllowOther: true}
	data, _ := json.Marshal(request)
	created := performJSON(t, engine, "POST", "/v1/conversations/"+conv.ID+"/human", string(data))
	if created.StatusCode() != 200 {
		t.Fatalf("create %d %s", created.StatusCode(), created.Body())
	}
	var question loopd.HumanResult
	if err := json.Unmarshal(created.Body(), &question); err != nil {
		t.Fatal(err)
	}
	path := "/v1/conversations/" + conv.ID + "/replies"
	payload := `{"reply_to_id":"` + question.Message.ID + `","outcome":"success","value":"custom","user_key":"alice"}`
	actor = "mallory"
	forbidden := performJSON(t, engine, "POST", path, payload)
	if forbidden.StatusCode() != 403 {
		t.Fatalf("untrusted actor=%d %s", forbidden.StatusCode(), forbidden.Body())
	}
	actor = "alice"
	missing := performJSON(t, engine, "POST", path, `{"outcome":"success","value":"custom"}`)
	if missing.StatusCode() != 400 {
		t.Fatalf("missing reference=%d", missing.StatusCode())
	}
	accepted := performJSON(t, engine, "POST", path, payload)
	if accepted.StatusCode() != 200 {
		t.Fatalf("reply=%d %s", accepted.StatusCode(), accepted.Body())
	}
	var result loopd.HumanResult
	if err := json.Unmarshal(accepted.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "custom" || result.Reply.ReplyToID != question.Message.ID {
		t.Fatalf("result=%+v", result)
	}
	taskAfter, err := store.GetDeliveryInput(ctx, taskID)
	if err != nil || taskAfter.ID != task.ID {
		t.Fatalf("changed context=%+v %v", taskAfter, err)
	}
	// +case=`Questions are conversation-owned even without a UI delivery ID.`
	request.TaskID = ""
	request.EffectKey = "independent"
	data, _ = json.Marshal(request)
	independent := performJSON(t, engine, "POST", "/v1/conversations/"+conv.ID+"/human", string(data))
	if independent.StatusCode() != 200 {
		t.Fatalf("independent=%d %s", independent.StatusCode(), independent.Body())
	}
	if err := json.Unmarshal(independent.Body(), &question); err != nil {
		t.Fatal(err)
	}
	if question.Message.TaskID != "" {
		t.Fatalf("unexpected delivery dependency: %+v", question)
	}
	dismissed := performJSON(t, engine, "POST", "/v1/conversations/"+conv.ID+"/replies",
		`{"reply_to_id":"`+question.Message.ID+`","outcome":"dismissed"}`)
	if dismissed.StatusCode() != 200 {
		t.Fatalf("dismiss independent=%d %s", dismissed.StatusCode(), dismissed.Body())
	}
}
func TestBrowserIdentityIsAnOpaqueCredential(t *testing.T) {
	a := hertzapp.NewContext(0)
	alice, err := browserIdentity(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	cookie := string(a.Response.Header.Peek("Set-Cookie"))
	if cookie == "" {
		t.Fatal("missing browser cookie")
	}
	b := hertzapp.NewContext(0)
	b.Request.Header.Set("Cookie", cookie)
	again, err := browserIdentity(context.Background(), b)
	if err != nil || again != alice {
		t.Fatalf("cookie did not restore identity: %v", err)
	}
	c := hertzapp.NewContext(0)
	c.Request.Header.Set("Cookie", "loopd-human="+alice)
	forged, err := browserIdentity(context.Background(), c)
	if err != nil || forged == alice {
		t.Fatal("public actor key authenticated a different browser")
	}
}
