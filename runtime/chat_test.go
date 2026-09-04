package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
)

func TestChatSendStartsAndResumesOneTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/conversations/conversation-1/messages" {
			http.NotFound(response, request)
			return
		}
		var input SendMessageRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.TaskID != "" && request.Header.Get("Last-Event-ID") != "2-0" {
			t.Errorf("Last-Event-ID = %q, want 2-0", request.Header.Get("Last-Event-ID"))
		}
		response.Header().Set(taskIDHeader, "task-1")
		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(response, "data: {\"op\":\"start\",\"seq\":2,\"model\":{\"version\":\"1.0\",\"biz\":\"chat\",\"meta\":{},\"blocks\":[]}}\n\n")
		_, _ = io.WriteString(response, "id: 4-0\ndata: {\"op\":\"end\",\"seq\":3}\n\n")
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := runtime.Loop.Chat.Send(context.Background(), "conversation-1", SendMessageRequest{
		TaskID: "task-1",
	}, "2-0")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.TaskID() != "task-1" {
		t.Fatalf("task ID = %q", stream.TaskID())
	}
	first, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := agentueui.Parse(first.Data)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "2-0" || parsed.Op != agentueui.OpStart {
		t.Fatalf("first event = %#v, parsed=%#v", first, parsed)
	}
	last, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	ended, err := IsEnd(last)
	if err != nil || !ended {
		t.Fatalf("last event = %#v, ended=%t, error=%v", last, ended, err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("stream tail error = %v, want EOF", err)
	}
}

func TestChatPublishesAndCompletesTask(t *testing.T) {
	var published, completed bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/events"):
			published = true
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"id":"2-0"}`)
		case strings.HasSuffix(request.URL.Path, "/complete"):
			completed = true
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	event, err := runtime.Loop.Chat.Emit(context.Background(), "task-1", agentueui.Event{
		Op: agentueui.OpSet, Seq: 2,
		Block: map[string]any{"id": "answer", "type": "text", "content": "done"},
	})
	if err != nil || event.ID != "2-0" {
		t.Fatalf("Emit event=%#v error=%v", event, err)
	}
	if err := runtime.Loop.Chat.Complete(context.Background(), "task-1", nil); err != nil {
		t.Fatal(err)
	}
	if !published || !completed {
		t.Fatalf("published=%t completed=%t", published, completed)
	}
}
