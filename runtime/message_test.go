package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
)

// +case=`Speak defaults to an already-ended message; streaming handles restore revision, retry the same seq, and End idempotently.`
func TestSpeakHandleModesAndRecovery(t *testing.T) {
	ctx := context.Background()
	snapshot := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	value := contract.Message{Status: contract.MessageStatusStreaming, ID: "output", Revision: 7, Content: snapshot}
	var seqs []uint64
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/conversations/conv/messages":
			var input contract.SpeakRequest
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.Status != contract.MessageStatusStreaming {
				_ = json.NewEncoder(w).Encode(contract.Message{Status: contract.MessageStatusCompleted, ID: "once", Revision: 1, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)})
			} else {
				_ = json.NewEncoder(w).Encode(value)
			}
		case "/v1/messages/output/events":
			var input struct{ Event ui.Event }
			_ = json.NewDecoder(r.Body).Decode(&input)
			seqs = append(seqs, input.Event.Seq)
			if fail {
				fail = false
				http.Error(w, "try again", 503)
				return
			}
			value.Revision = input.Event.Seq
			if input.Event.Op == ui.OpEnd {
				value.Status = contract.MessageStatusCompleted
			}
			fmt.Fprint(w, `{"id":"cursor"}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	runtime, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	once, err := runtime.Loop.Conv.Speak(ctx, "conv", contract.SpeakRequest{Key: "once"})
	if err != nil || once.ID() != "once" {
		t.Fatalf("once=%v err=%v", once, err)
	}
	if _, ok := once.(Stream); ok {
		t.Fatal("Speak returned write capability")
	}
	event := ui.Event{Seq: 999, Op: ui.OpSet, Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}

	stream, err := runtime.Loop.Conv.Tell(ctx, "conv", contract.SpeakRequest{Key: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := runtime.Loop.Conv.Tell(ctx, "conv", contract.SpeakRequest{Key: "stream"})
	if err != nil || again.ID() != stream.ID() {
		t.Fatalf("handle not shared: %v", err)
	}
	if err := stream.Emit(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := stream.End(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stream.End(ctx); err != nil {
		t.Fatal(err)
	}
	if err := stream.Emit(ctx, event); err == nil {
		t.Fatal("write after End")
	}
	if !reflect.DeepEqual(seqs, []uint64{8, 8, 9}) {
		t.Fatalf("sequences=%v", seqs)
	}
	restored, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	stream, err = restored.Loop.Conv.Tell(ctx, "conv", contract.SpeakRequest{Key: "stream"})
	if err != nil || stream.ID() != "output" {
		t.Fatalf("restore: %v", err)
	}
	if err := stream.Emit(ctx, event); err == nil || len(seqs) != 3 {
		t.Fatalf("restored writer accepted output after End: %v", err)
	}
	if err := stream.End(ctx); err != nil || len(seqs) != 3 {
		t.Fatalf("restored End=%v seqs=%v", err, seqs)
	}
}

// +case=`An exhausted ambiguous update cannot be skipped by another Emit, End, or Speak refresh.`
func TestMessageKeepsUnconfirmedUpdate(t *testing.T) {
	ctx := context.Background()
	value := contract.Message{Status: contract.MessageStatusStreaming, ID: "stream", Revision: 1, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)}
	attempts, fail := 0, true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/conversations/conv/messages" {
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		var body struct{ Event ui.Event }
		_ = json.NewDecoder(r.Body).Decode(&body)
		attempts++
		if body.Event.Seq != 2 {
			t.Errorf("skipped ambiguous update: %d", body.Event.Seq)
		}
		value.Revision = body.Event.Seq // accepted, but response may be lost
		if fail {
			http.Error(w, "response lost", 503)
			return
		}
		w.WriteHeader(202)
	}))
	defer server.Close()
	rt, err := New(server.URL, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	request := contract.SpeakRequest{Key: "step"}
	stream, err := rt.Loop.Conv.Tell(ctx, "conv", request)
	if err != nil {
		t.Fatal(err)
	}
	update := ui.Event{Op: ui.OpSet, Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}
	if err := stream.Emit(ctx, update); err == nil || attempts != 3 {
		t.Fatalf("retry budget: attempts=%d err=%v", attempts, err)
	}
	same := stream
	err = nil
	if err != nil || same != stream {
		t.Fatalf("refresh: %v", err)
	}
	if err := same.End(ctx); err == nil || attempts != 3 {
		t.Fatal("End skipped pending update")
	}
	fail = false
	if err := stream.Emit(ctx, update); err != nil || attempts != 4 {
		t.Fatalf("retry pending: attempts=%d err=%v", attempts, err)
	}
}

func TestMessageEndStatus(t *testing.T) {
	for _, status := range []contract.MessageStatus{contract.MessageStatusFailed, contract.MessageStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			value := contract.Message{ID: "stream", Status: contract.MessageStatusStreaming, Revision: 1, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)}
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/conversations/conv/messages" {
					_ = json.NewEncoder(w).Encode(value)
					return
				}
				var input struct {
					Event  json.RawMessage
					Status contract.MessageStatus
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				event, err := ui.Parse(input.Event)
				if err != nil || event.Op != ui.OpEnd || input.Status != status {
					t.Errorf("invalid End: %+v %v", input, err)
				}
				value.Status, value.Revision = input.Status, event.Seq
				writes++
				w.WriteHeader(202)
			}))
			defer server.Close()
			rt, err := New(server.URL, Options{HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			ctx := context.Background()
			stream, err := rt.Loop.Conv.Tell(ctx, "conv", contract.SpeakRequest{Key: "step"})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.End(ctx, contract.MessageStatusStreaming); err == nil {
				t.Fatal("accepted nonterminal End")
			}
			for i := 0; i < 2; i++ {
				if err := stream.End(ctx, status); err != nil {
					t.Fatal(err)
				}
			}
			if writes != 1 || value.Status != status {
				t.Fatalf("writes=%d status=%s", writes, value.Status)
			}
			if err := stream.End(ctx); err == nil {
				t.Fatal("replaced failure/cancellation with success")
			}
		})
	}
}
