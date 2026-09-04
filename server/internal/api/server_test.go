package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

func TestChatHTTPFlow(t *testing.T) {
	store, err := repo.Open(repo.Config{Path: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := New(
		service.NewConversationService(store, nil),
		service.NewMessageService(store, nil),
		service.NewChatService(store, nopTaskClient{}, nil),
		service.NewTaskService(store, nil),
		nil,
	)
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)

	created := performJSON(t, engine, "POST", "/v1/conversations", `{"name":"Planning"}`)
	if created.StatusCode() != 201 {
		t.Fatalf("create conversation status=%d body=%s", created.StatusCode(), created.Body())
	}
	var conversation loopd.Conversation
	if err := json.Unmarshal(created.Body(), &conversation); err != nil {
		t.Fatal(err)
	}

	sent := performJSON(t, engine, "POST", "/v1/conversations/"+conversation.ID+"/messages", `{
		"user_key":"user-1",
		"responder":{"kind":"operator","key":"intent"},
		"content":{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"hello"}]}
	}`)
	if sent.StatusCode() != 201 {
		t.Fatalf("send status=%d body=%s", sent.StatusCode(), sent.Body())
	}
	var answer loopd.Message
	if err := json.Unmarshal(sent.Body(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Kind != loopd.RoleOperator || answer.Key != "intent" || answer.TaskID == "" {
		t.Fatalf("answer = %#v", answer)
	}
	taskResponse := ut.PerformRequest(engine, "GET", "/v1/tasks/"+answer.TaskID, nil).Result()
	if taskResponse.StatusCode() != 200 {
		t.Fatalf("task status=%d body=%s", taskResponse.StatusCode(), taskResponse.Body())
	}
	var task loopd.TaskContext
	if err := json.Unmarshal(taskResponse.Body(), &task); err != nil {
		t.Fatal(err)
	}
	if task.ID != answer.TaskID || task.Input.Kind != loopd.RoleUser || task.Response.ID != answer.ID {
		t.Fatalf("task = %#v", task)
	}

	history := ut.PerformRequest(engine, "GET", "/v1/conversations/"+conversation.ID+"/messages", nil).Result()
	if history.StatusCode() != 200 {
		t.Fatalf("history status=%d body=%s", history.StatusCode(), history.Body())
	}
	var result page[loopd.Message]
	if err := json.Unmarshal(history.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 || result.Data[0].TaskID != answer.TaskID || result.Data[1].ID != answer.ID {
		t.Fatalf("history = %#v", result.Data)
	}
	if response := ut.PerformRequest(engine, "GET", "/v1/responders", nil).Result(); response.StatusCode() != 404 {
		t.Fatalf("responders status=%d, want 404", response.StatusCode())
	}
}

type nopTaskClient struct{}

func (nopTaskClient) Create(context.Context, string, loopd.ResponderRef) error { return nil }
func (nopTaskClient) Delete(context.Context, string) error                     { return nil }

func performJSON(t *testing.T, engine *route.Engine, method, path, value string) *protocol.Response {
	t.Helper()
	return ut.PerformRequest(engine, method, path, &ut.Body{Body: strings.NewReader(value), Len: len(value)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
}
