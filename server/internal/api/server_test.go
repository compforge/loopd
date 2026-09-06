package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/component"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/compforge/loopd/server/internal/view"
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
	var conversation contract.Conversation
	if err := json.Unmarshal(created.Body(), &conversation); err != nil {
		t.Fatal(err)
	}
	if conversation.ActorKind != contract.ActorKindUser || conversation.ActorKey == "" || conversation.ParentID != "" {
		t.Fatalf("user conversation = %+v", conversation)
	}
	listed := ut.PerformRequest(engine, "GET", "/v1/conversations", nil).Result()
	if listed.StatusCode() != 200 {
		t.Fatalf("list conversations status=%d body=%s", listed.StatusCode(), listed.Body())
	}
	var conversations view.Page[contract.Conversation]
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
	var result view.Page[contract.Message]
	if err := json.Unmarshal(history.Body(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].TaskID != taskID || result.Data[0].ID != input.ID {
		t.Fatalf("history = %#v", result.Data)
	}

	childResponse := ut.PerformRequest(engine, "GET", "/v1/conversations?parent_id="+conversation.ID+"&actor_kind=operator&actor_key=intent", nil).Result()
	if childResponse.StatusCode() != 200 {
		t.Fatalf("create detail=%s", childResponse.Body())
	}
	var children view.Page[contract.Conversation]
	if err := json.Unmarshal(childResponse.Body(), &children); err != nil {
		t.Fatal(err)
	}
	if len(children.Data) != 1 {
		t.Fatalf("automatic detail allocation: %+v", children)
	}
	child := children.Data[0]
	if child.ParentID != conversation.ID || child.ActorKind != contract.ActorKindOperator || child.ActorKey != "intent" {
		t.Fatalf("work conversation=%+v", child)
	}
	_, err = server.messages.CreateMessage(context.Background(), child.ID, taskID, contract.ActorKindHarness, "call-1",
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
		var page view.Page[contract.Conversation]
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

type convStreamRunner struct {
	completedChatRunner
	t      *testing.T
	convID string
}

func (runner convStreamRunner) Listen(_ context.Context, convID string, deliver func(component.Event) error) error {
	if convID != runner.convID {
		runner.t.Fatalf("stream conv = %q", convID)
	}
	m := contract.Message{ID: "message", ConversationID: convID, Status: contract.MessageStatusStreaming, Kind: contract.ActorKindOperator, Key: "router", Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)}
	return deliver(component.Event{MessageID: m.ID, Message: &m, Data: json.RawMessage(`{"op":"start","seq":1,"model":{"version":"1.1","biz":"chat","meta":{},"blocks":[]}}`)})
}

func TestConversationStreamHTTPWithoutUserInput(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "stream.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateConversation(context.Background(), model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	server := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil),
		service.NewChatService(store, completedChatRunner{}, nil, nil), nil)
	server.Listen = (convStreamRunner{t: t, convID: "conv"}).Listen
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)
	request := hertzapp.NewContext(1)
	request.Request.Header.SetMethod("GET")
	request.Request.SetRequestURI("/v1/conversations/conv/stream")
	writer := &streamWriter{}
	request.Response.HijackWriter(writer)
	engine.ServeHTTP(context.Background(), request)
	if request.Response.StatusCode() != 200 || !strings.Contains(writer.String(), `"message_id":"message"`) || !strings.Contains(string(request.Response.Header.ContentType()), "text/event-stream") {
		t.Fatalf("stream response: %d %s", request.Response.StatusCode(), writer.String())
	}
	missing := ut.PerformRequest(engine, "GET", "/v1/conversations/missing/stream", nil).Result()
	if missing.StatusCode() != 404 {
		t.Fatalf("missing conv status=%d", missing.StatusCode())
	}
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

func performJSON(t *testing.T, engine *route.Engine, method, path, value string) *protocol.Response {
	t.Helper()
	return ut.PerformRequest(engine, method, path, &ut.Body{Body: strings.NewReader(value), Len: len(value)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
}

func (completedChatRunner) EmitMessage(context.Context, string, json.RawMessage, ...contract.MessageStatus) (string, error) {
	return "", nil
}

type unavailableChatRunner struct{ completedChatRunner }

// +case=`An accepted input returns its message and receipt even when opening the page stream fails.`
func TestChatAcknowledgesInputBeforePageBridge(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "chat.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := New(service.NewActorService(store, nil), service.NewConversationService(store, nil),
		service.NewMessageService(store, nil), service.NewChatService(store, unavailableChatRunner{}, nil, nil), nil)
	server.Listen = func(context.Context, string, func(component.Event) error) error {
		return errors.New("page bridge unavailable")
	}
	engine := route.NewEngine(config.NewOptions(nil))
	server.Register(engine)
	created := performJSON(t, engine, "POST", "/v1/conversations", `{"name":"offline bridge"}`)
	var conv contract.Conversation
	if err := json.Unmarshal(created.Body(), &conv); err != nil {
		t.Fatal(err)
	}
	id, body := performChat(t, server, conv.ID, `{"user_key":"alice","target":{"kind":"operator","key":"a"},"content":{"version":"1.0","biz":"chat","meta":{},"blocks":[]}}`)
	input, err := store.GetDeliveryInput(context.Background(), id)
	if err != nil || !strings.Contains(body, input.ID) || !strings.Contains(body, `"op":"start"`) {
		t.Fatalf("missing acceptance: task=%s body=%s err=%v", id, body, err)
	}
}
