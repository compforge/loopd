package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

// +case=`Message discovery returns metadata; explicit content and block reads preserve conversation scope and logical content.`
func TestMessageReadHTTP(t *testing.T) {
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "read.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, id := range []string{"conv", "other"} {
		if _, err := store.CreateConversation(ctx, model.Conversation{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		status := contract.MessageStatusCompleted
		if id == "c" {
			status = contract.MessageStatusStreaming
		}
		_, err := store.CreateMessage(ctx, model.Message{ID: id, ConversationID: "conv", SourceKind: "operator", SourceKey: "writer", Status: string(status), Revision: 1, Content: []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"text","type":"text","content":"hello"}]}`)})
		if err != nil {
			t.Fatal(err)
		}
	}
	s := New(service.NewActorService(store, nil), service.NewConversationService(store, nil), service.NewMessageService(store, nil, nil), service.NewChatService(store, nil, nil), nil)
	engine := route.NewEngine(config.NewOptions(nil))
	s.Register(engine)
	response := performJSON(t, engine, "GET", "/v1/conversations/conv/messages?before=d&after=a&order=desc&limit=1", "")
	var page contract.MessagePage
	if err := json.Unmarshal(response.Body(), &page); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode() != 200 || len(page.Data) != 1 || page.Data[0].ID != "c" || page.Next != "c" || strings.Contains(string(response.Body()), "content") {
		t.Fatalf("page=%s", response.Body())
	}
	response = performJSON(t, engine, "GET", "/v1/conversations/conv/messages?ids=b,c&status=completed", "")
	if err := json.Unmarshal(response.Body(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "b" {
		t.Fatalf("filter=%s", response.Body())
	}
	for _, path := range []string{"?order=invalid", "?status=invalid", "?limit=0"} {
		if r := performJSON(t, engine, "GET", "/v1/conversations/conv/messages"+path, ""); r.StatusCode() != 400 {
			t.Fatalf("invalid query %s: %d", path, r.StatusCode())
		}
	}
	for _, suffix := range []string{"", "/content", "/blocks", "/blocks/text"} {
		if r := performJSON(t, engine, "GET", "/v1/conversations/other/messages/b"+suffix, ""); r.StatusCode() != 404 {
			t.Fatalf("scope %s: %d", suffix, r.StatusCode())
		}
	}
	for _, suffix := range []string{"/content", "/blocks", "/blocks/text"} {
		r := performJSON(t, engine, "GET", "/v1/conversations/conv/messages/b"+suffix, "")
		if r.StatusCode() != 200 || !strings.Contains(string(r.Body()), "hello") {
			t.Fatalf("read %s: %d %s", suffix, r.StatusCode(), r.Body())
		}
	}
	if r := performJSON(t, engine, "GET", "/v1/conversations/conv/messages/b/blocks?cursor=bad", ""); r.StatusCode() != 400 {
		t.Fatalf("bad cursor=%d", r.StatusCode())
	}
	if r := performJSON(t, engine, "GET", "/v1/conversations/conv/messages/b/blocks?cursor=2:0", ""); r.StatusCode() != 409 {
		t.Fatalf("revision conflict=%d", r.StatusCode())
	}

	// +case=`Accepted large blocks remain readable through logical block and snapshot APIs; physical frames never escape storage.`
	for _, size := range []int{2 << 20, 9 << 20} {
		data, err := json.Marshal(map[string]any{"version": "1.1", "biz": "chat", "meta": map[string]any{}, "blocks": []any{
			map[string]any{"id": "large", "type": "text", "content": strings.Repeat("x", size)},
		}})
		if err != nil {
			t.Fatal(err)
		}
		message, err := store.CreateMessage(ctx, model.Message{ID: "large", ConversationID: "other", SourceKind: "operator", SourceKey: "writer", Status: "completed", Revision: 1, Content: data})
		if err != nil {
			t.Fatal(err)
		}
		path := "/v1/conversations/other/messages/" + message.ID
		if r := performJSON(t, engine, "GET", path+"/blocks/large", ""); r.StatusCode() != 200 {
			t.Fatalf("large block=%d", r.StatusCode())
		}
		if r := performJSON(t, engine, "GET", path+"/content", ""); r.StatusCode() != 200 {
			t.Fatalf("large snapshot=%d", r.StatusCode())
		}
		if err := store.DeleteMessage(ctx, message.ID); err != nil {
			t.Fatal(err)
		}
	}
}
