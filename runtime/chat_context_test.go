package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loopd "github.com/compforge/loopd"
)

func TestChatMessagesReadsVisiblePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/tasks/task%2F1/messages" || r.URL.Query().Get("after") != "message/1" || r.URL.Query().Get("limit") != "2" {
			t.Errorf("unexpected read request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(page[loopd.Message]{Data: []loopd.Message{
			{ID: "m2", TaskID: "task/1", ConversationID: "root", Kind: loopd.RoleUser, Key: "alice"},
			{ID: "m3", TaskID: "task/1", ConversationID: "work", Kind: loopd.RoleHarness, Key: "call"},
		}})
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	messages, err := runtime.Loop.Chat.Messages(context.Background(), "task/1", "message/1", 2)
	if err != nil || len(messages) != 2 || messages[0].ConversationID != "root" || messages[1].ConversationID != "work" {
		t.Fatalf("task messages = %+v, error = %v", messages, err)
	}
}
