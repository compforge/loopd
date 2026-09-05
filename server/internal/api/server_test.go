package api

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/cloudwego/hertz/pkg/route/param"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/delivery"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

func TestChatHTTPFlow(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := New(
		service.NewActorService(store, nil),
		service.NewConversationService(store, nil),
		service.NewMessageService(store, nil),
		service.NewChatService(store, completedChatRunner{}, nil, nil),
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
	if conversation.ActorKind != loopd.RoleUser || conversation.ActorKey == "" || conversation.ParentID != "" {
		t.Fatalf("user conversation = %+v", conversation)
	}
	listed := ut.PerformRequest(engine, "GET", "/v1/conversations", nil).Result()
	if listed.StatusCode() != 200 {
		t.Fatalf("list conversations status=%d body=%s", listed.StatusCode(), listed.Body())
	}
	var conversations page[loopd.Conversation]
	if err := json.Unmarshal(listed.Body(), &conversations); err != nil {
		t.Fatal(err)
	}
	if len(conversations.Data) != 1 || conversations.Data[0].ID != conversation.ID {
		t.Fatalf("conversations = %#v", conversations.Data)
	}

	taskID, stream := performChat(t, server, conversation.ID, `{
		"user_key":"user-1",
		"target":{"kind":"operator","key":"intent"},
		"content":{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"q","type":"text","content":"hello"}]}
	}`)
	if !strings.Contains(stream, `"op":"start"`) || !strings.Contains(stream, `"op":"end"`) {
		t.Fatalf("send body=%s, want AgentUE start and end events", stream)
	}
	if taskID == "" {
		t.Fatal("send response omitted task ID header")
	}
	input, err := store.GetDeliveryInput(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	history := ut.PerformRequest(engine, "GET", "/v1/conversations/"+conversation.ID+"/messages", nil).Result()
	if history.StatusCode() != 200 {
		t.Fatalf("history status=%d body=%s", history.StatusCode(), history.Body())
	}
	var result page[loopd.Message]
	if err := json.Unmarshal(history.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].TaskID != taskID || result.Data[0].ID != input.ID {
		t.Fatalf("history = %#v", result.Data)
	}

	childResponse := performJSON(t, engine, "POST", "/v1/conversations/"+conversation.ID+"/actors", `{"kind":"operator","key":"intent"}`)
	if childResponse.StatusCode() != 200 {
		t.Fatalf("create detail=%s", childResponse.Body())
	}
	var child loopd.Conversation
	if err := json.Unmarshal(childResponse.Body(), &child); err != nil {
		t.Fatal(err)
	}
	if child.ParentID != conversation.ID || child.ActorKind != loopd.RoleOperator || child.ActorKey != "intent" {
		t.Fatalf("work conversation=%+v", child)
	}
	_, err = server.messages.CreateMessage(context.Background(), child.ID, taskID, loopd.RoleHarness, "call-1",
		json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[{"id":"answer","type":"text","content":"detail output"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query      string
		expectedID string
	}{
		{"", conversation.ID},
		{"?parent_id=" + conversation.ID + "&actor_kind=operator&actor_key=intent", child.ID},
		{"?parent_id=missing&actor_kind=operator&actor_key=intent", ""},
	} {
		response := ut.PerformRequest(engine, "GET", "/v1/conversations"+test.query, nil).Result()
		if response.StatusCode() != 200 {
			t.Fatalf("query %s: %s", test.query, response.Body())
		}
		var page page[loopd.Conversation]
		if err := json.Unmarshal(response.Body(), &page); err != nil {
			t.Fatal(err)
		}
		if test.expectedID == "" {
			if len(page.Data) != 0 {
				t.Fatalf("unexpected details=%+v", page.Data)
			}
		} else if len(page.Data) != 1 || page.Data[0].ID != test.expectedID {
			t.Fatalf("query %s: %+v", test.query, page.Data)
		}
	}
	childMessages := ut.PerformRequest(engine, "GET", "/v1/conversations/"+child.ID+"/messages", nil).Result()
	if childMessages.StatusCode() != 200 || !strings.Contains(string(childMessages.Body()), "detail output") {
		t.Fatalf("child messages=%s", childMessages.Body())
	}
	if removed := performJSON(t, engine, "GET", "/v1/tasks/"+taskID, ""); removed.StatusCode() != 404 {
		t.Fatalf("legacy task endpoint survived: %d", removed.StatusCode())
	}
}

type completedChatRunner struct{}

func (completedChatRunner) Initialize(context.Context, string, json.RawMessage) error { return nil }
func (completedChatRunner) Delete(context.Context, string) error                      { return nil }
func (completedChatRunner) Emit(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}

type streamWriter struct{ bytes.Buffer }

func (writer *streamWriter) Flush() error    { return nil }
func (writer *streamWriter) Finalize() error { return nil }

func performChat(t *testing.T, server *Server, conversationID, body string) (string, string) {
	t.Helper()
	request := hertzapp.NewContext(1)
	request.Params = param.Params{{Key: "conversation_id", Value: conversationID}}
	request.Request.SetBodyString(body)
	request.Request.Header.Set("Content-Type", "application/json")
	writer := &streamWriter{}
	request.Response.HijackWriter(writer)
	if err := server.createChatMessages(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	return string(request.Response.Header.Peek(taskIDHeader)), writer.String()
}
func (completedChatRunner) Complete(context.Context, string, *delivery.Failure) error { return nil }
func (completedChatRunner) Stream(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	deliver func(delivery.Event) error,
) error {
	if err := deliver(delivery.Event{ID: "1-0", Data: json.RawMessage(`{"op":"start","seq":1,"model":{"version":"1.0","biz":"chat","meta":{},"blocks":[]}}`), Persisted: true}); err != nil {
		return err
	}
	return deliver(delivery.Event{ID: "2-0", Data: json.RawMessage(`{"op":"end","seq":2}`), Persisted: true})
}

func performJSON(t *testing.T, engine *route.Engine, method, path, value string) *protocol.Response {
	t.Helper()
	return ut.PerformRequest(engine, method, path, &ut.Body{Body: strings.NewReader(value), Len: len(value)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
}

func (completedChatRunner) EmitMessage(context.Context, string, json.RawMessage) (string, error) {
	return "", nil
}
