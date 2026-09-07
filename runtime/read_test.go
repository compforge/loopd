package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/compforge/loopd/pkg/contract"
)

// +case=`Resolving a Call's Message does not load output; successful Get selects only its result block.`
func TestCallSharesLazyMessage(t *testing.T) {
	metadata, bodies := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/harness/runs":
			json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", MessageID: "m", ConversationID: "conv"})
		case "/v1/harness/runs/run":
			metadata++
			json.NewEncoder(w).Encode(contract.HarnessCall{ID: "run", MessageID: "m", ConversationID: "conv", Phase: contract.CallSucceeded})
		case "/v1/conversations/conv/messages/m/blocks/result":
			bodies++
			json.NewEncoder(w).Encode(contract.BlockSnapshot{Revision: 2, Block: json.RawMessage(`{"id":"result","type":"result","format":"text","content":"answer"}`)})
		default:
			t.Errorf("unexpected body read %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	rt, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()
	call, err := rt.Loop.Harness.Prompt(ctx, Prompt{ConversationID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := call.Message(ctx)
	if err != nil || message.ID() != "m" || metadata != 0 || bodies != 0 {
		t.Fatalf("prompt reference: %v", err)
	}
	restored := rt.Loop.Harness.Call("run")
	message, err = restored.Message(ctx)
	if err != nil || message.ConversationID() != "conv" || metadata != 1 || bodies != 0 {
		t.Fatalf("restored reference: %v", err)
	}
	value, err := restored.Get(ctx)
	if err != nil || value.Result.Text() != "answer" || metadata != 2 || bodies != 1 {
		t.Fatalf("result=%+v metadata=%d bodies=%d err=%v", value, metadata, bodies, err)
	}
}

// +case=`Read/ID never access the server; List loads metadata only, and content reads are explicit and scoped.`
func TestLazyMessageReads(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/v1/conversations/conv/messages":
			q := r.URL.Query()
			if q.Get("ids") != "m" || q.Get("before") != "z" || q.Get("order") != "desc" || q.Get("status") != "completed" {
				t.Errorf("query=%s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(contract.MessagePage{Data: []contract.MessageInfo{{ID: "m", ConversationID: "conv", Revision: 1}}})
		case "/v1/conversations/conv/messages/m":
			json.NewEncoder(w).Encode(contract.MessageInfo{ID: "m", ConversationID: "conv", Revision: 2})
		case "/v1/conversations/conv/messages/m/content":
			json.NewEncoder(w).Encode(contract.Message{ID: "m", Revision: 2, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)})
		case "/v1/conversations/conv/messages/m/blocks/result":
			json.NewEncoder(w).Encode(contract.BlockSnapshot{Revision: 2, Block: json.RawMessage(`{"id":"result","type":"text","content":"ok"}`)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	rt, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx := context.Background()
	message := rt.Loop.Conv.Read("conv", "m")
	if message.ID() != "m" || message.ConversationID() != "conv" || calls != 0 {
		t.Fatal("reference caused I/O")
	}
	page, err := rt.Loop.Conv.List(ctx, "conv", MessageQuery{IDs: []string{"m"}, Before: "z", Order: Desc, Statuses: []contract.MessageStatus{contract.MessageStatusCompleted}})
	if err != nil || len(page.Messages) != 1 || calls != 1 {
		t.Fatalf("list=%+v calls=%d err=%v", page, calls, err)
	}
	info, err := page.Messages[0].Info(ctx)
	if err != nil || info.Revision != 2 || page.Infos[0].Revision != 1 {
		t.Fatalf("fresh info=%+v err=%v", info, err)
	}
	snapshot, err := message.Snapshot(ctx)
	if err != nil || len(snapshot.Content) == 0 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	block, err := message.Block(ctx, "result")
	if err != nil || block.Revision != 2 {
		t.Fatalf("block=%+v err=%v", block, err)
	}
	_, err = rt.Loop.Conv.Read("another", "m").Info(ctx)
	if err == nil {
		t.Fatal("cross-conversation reference resolved")
	}
}

// +case=`Block iteration stops on revision conflict without replaying visitor side effects.`
func TestMessageBlocksConflictStopsIteration(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			json.NewEncoder(w).Encode(contract.BlockPage{Revision: 1, Data: []json.RawMessage{json.RawMessage(`{"id":"a","type":"text","content":"a"}`)}, Next: "1:1"})
		} else {
			if r.URL.Query().Get("cursor") != "1:1" {
				t.Errorf("cursor=%s", r.URL.RawQuery)
			}
			http.Error(w, "revision changed", http.StatusConflict)
		}
	}))
	defer server.Close()
	rt, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	visited := 0
	err = rt.Loop.Conv.Read("conv", "m").Blocks(context.Background(), func(b BlockSnapshot) error { visited++; return nil })
	if !IsConflict(err) || visited != 1 || calls != 2 {
		t.Fatalf("calls=%d visited=%d err=%v", calls, visited, err)
	}
}
