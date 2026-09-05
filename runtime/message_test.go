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
	loopd "github.com/compforge/loopd"
)

// +case=`Speak defaults to an already-ended message; streaming handles restore revision, retry the same seq, and End idempotently.`
func TestSpeakHandleModesAndRecovery(t *testing.T) {
	ctx := context.Background()
	snapshot := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{"output":{"ended":false}},"blocks":[]}`)
	value := loopd.Message{ID: "output", Revision: 7, Content: snapshot}
	var seqs []uint64
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/conversations/conv/speak":
			var input loopd.SpeakRequest
			_ = json.NewDecoder(r.Body).Decode(&input)
			if !input.Stream {
				_ = json.NewEncoder(w).Encode(loopd.Message{ID: "once", Revision: 1, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{"output":{"ended":true}},"blocks":[]}`)})
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
				value.Content = json.RawMessage(`{"version":"1.0","biz":"chat","meta":{"output":{"ended":true}},"blocks":[]}`)
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
	once, err := runtime.Loop.Conv.Speak(ctx, "conv", loopd.SpeakRequest{Key: "once"})
	if err != nil || !once.Value().Ended() {
		t.Fatalf("once=%v err=%v", once, err)
	}
	if err := once.End(ctx); err != nil {
		t.Fatal(err)
	}
	event := ui.Event{Seq: 999, Op: ui.OpSet, Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}
	if err := once.Emit(ctx, event); err == nil {
		t.Fatal("one-shot message stayed writable")
	}
	stream, err := runtime.Loop.Conv.Speak(ctx, "conv", loopd.SpeakRequest{Stream: true, Key: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := runtime.Loop.Conv.Speak(ctx, "conv", loopd.SpeakRequest{Stream: true, Key: "stream"})
	if err != nil || again != stream {
		t.Fatalf("handle not shared: %v", err)
	}
	if err := again.Emit(ctx, event); err != nil {
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
	stream, err = restored.Loop.Conv.Speak(ctx, "conv", loopd.SpeakRequest{Stream: true, Key: "stream"})
	if err != nil || stream.ID() != "output" || !stream.Value().Ended() {
		t.Fatalf("restore: %v", err)
	}
	if err := stream.End(ctx); err != nil || len(seqs) != 3 {
		t.Fatalf("restored End=%v seqs=%v", err, seqs)
	}
}

// +case=`An exhausted ambiguous update cannot be skipped by another Emit, End, or Speak refresh.`
func TestMessageKeepsUnconfirmedUpdate(t *testing.T) {
	ctx := context.Background()
	value := loopd.Message{ID: "stream", Revision: 1, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{"output":{"ended":false}},"blocks":[]}`)}
	attempts, fail := 0, true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/conversations/conv/speak" {
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
	request := loopd.SpeakRequest{Key: "step", Stream: true}
	stream, err := rt.Loop.Conv.Speak(ctx, "conv", request)
	if err != nil {
		t.Fatal(err)
	}
	update := ui.Event{Op: ui.OpSet, Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}
	if err := stream.Emit(ctx, update); err == nil || attempts != 3 {
		t.Fatalf("retry budget: attempts=%d err=%v", attempts, err)
	}
	same, err := rt.Loop.Conv.Speak(ctx, "conv", request)
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
