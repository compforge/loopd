package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/redis/go-redis/v9"
)

func outputFixture(t *testing.T) (*repo.Store, *Coordinator, *Coordinator) {
	t.Helper()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "output.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	initial := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateChatMessages(ctx,
		model.Message{ID: "input", ConversationID: "root", TaskID: "task", Kind: "user", ActorKey: "human", Content: initial},
		model.Message{ID: "response", ConversationID: "root", TaskID: "task", Kind: "operator", ActorKey: "router", Content: initial}, nil)
	if err != nil {
		t.Fatal(err)
	}
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	bridge := agentuerunner.NewRedisEventBridge(client, agentuerunner.BridgeOptions{ReadBlock: time.Millisecond})
	producer, consumer := New(bridge, store, nil), New(bridge, store, nil)
	if err := producer.Initialize(ctx, "task", initial); err != nil {
		t.Fatal(err)
	}
	return store, producer, consumer
}
func outputRequest(key string) loopd.OutputRequest {
	return loopd.OutputRequest{Key: key, Actor: loopd.ActorRef{Kind: loopd.RoleHarness, Key: "same-actor"}}
}
func outputText(t *testing.T, seq uint64, text string) json.RawMessage {
	return marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: seq, Block: map[string]any{"id": "text", "type": "text", "content": text}})
}

// +case=`同一 Actor 多条输出可用相同 block ID 和 seq；重放按 Message 隔离且详情先于 Task end`
func TestOutputMessagesOwnIdentityAndReplay(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := producer.Output(ctx, "task", outputRequest("plan"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := producer.Output(ctx, "task", outputRequest("execute"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.ConversationID != b.ConversationID {
		t.Fatalf("outputs=%+v %+v", a, b)
	}
	for _, item := range []struct{ id, text string }{{a.ID, "plan"}, {b.ID, "execute"}} {
		event := outputText(t, 2, item.text)
		for i := 0; i < 2; i++ {
			if _, err := producer.EmitMessage(ctx, "task", item.id, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	// call_id is presentation metadata, never an implicit routing instruction.
	main := marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 2, Block: map[string]any{"id": "text", "type": "text", "content": "summary", "call_id": "not-a-destination"}})
	_, err = producer.Emit(ctx, "task", main)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BeginCompletion(ctx, "task", []byte("null"), false); err != nil {
		t.Fatal(err)
	}
	if err := producer.Complete(ctx, "task", nil); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	ends := map[string]bool{}
	err = consumer.Stream(ctx, "task", "root", "", func(value Event) error {
		e, err := agentueui.Parse(value.Data)
		if err != nil {
			return err
		}
		if e.Op == agentueui.OpSet {
			seen[value.MessageID], _ = e.Block["content"].(string)
		}
		if e.Op == agentueui.OpEnd {
			if value.MessageID == "" && (!ends[a.ID] || !ends[b.ID]) {
				t.Fatal("task end before output end")
			}
			ends[value.MessageID] = true
		}
		return nil
	})
	if err != nil || seen[a.ID] != "plan" || seen[b.ID] != "execute" || !ends[""] {
		t.Fatalf("seen=%v ends=%v err=%v", seen, ends, err)
	}
	for _, id := range []string{a.ID, b.ID, "response"} {
		row, err := store.GetMessage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var content struct{ Blocks []map[string]any }
		if err := json.Unmarshal(row.Content, &content); err != nil {
			t.Fatal(err)
		}
		if len(content.Blocks) != 1 {
			t.Fatalf("mixed content=%s", row.Content)
		}
	}
	if _, err := producer.EmitMessage(ctx, "task", a.ID, outputText(t, 3, "late")); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("late=%v", err)
	}
	if _, err := producer.Output(ctx, "task", outputRequest("late")); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("late output=%v", err)
	}
	if _, err := producer.EmitMessage(ctx, "another", a.ID, outputText(t, 3, "wrong")); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("scope=%v", err)
	}
	// All message snapshots survive loss of every Redis stream after retirement.
	if err := store.FinishCompletion(ctx, "task"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"task", "message/response", "message/" + a.ID, "message/" + b.ID} {
		if err := producer.events.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := consumer.Stream(ctx, "task", "root", "", func(value Event) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("closed snapshots+end=%d", count)
	}
}

func TestEnsureOutputConcurrentIdentity(t *testing.T) {
	_, producer, _ := outputFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < cap(ids); i++ {
		wg.Go(func() {
			message, err := producer.Output(context.Background(), "task", outputRequest("same"))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- message.ID
		})
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first != "" && first != id {
			t.Fatalf("duplicate %s %s", first, id)
		}
		first = id
	}
	if first == "" {
		t.Fatal("missing output")
	}
	changed := outputRequest("same")
	changed.Actor.Key = "other"
	if _, err := producer.Output(context.Background(), "task", changed); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("changed actor=%v", err)
	}
}

// A work message discovered after the stream starts must not be mistaken for the main response.
func TestStreamDiscoversOutputDuringTask(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created := ""
	observed := false
	err := consumer.Stream(ctx, "task", "root", "", func(value Event) error {
		e, err := agentueui.Parse(value.Data)
		if err != nil {
			return err
		}
		if created == "" && value.MessageID == "response" {
			message, err := producer.Output(ctx, "task", outputRequest("later"))
			if err != nil {
				return err
			}
			created = message.ID
			if _, err := producer.EmitMessage(ctx, "task", created, outputText(t, 2, "later")); err != nil {
				return err
			}
			if err := store.BeginCompletion(ctx, "task", []byte("null"), false); err != nil {
				return err
			}
			return producer.Complete(ctx, "task", nil)
		}
		if value.MessageID == created && e.Op == agentueui.OpSet {
			observed = true
		}
		if value.MessageID == "" && e.Op == agentueui.OpEnd && !observed {
			t.Fatal("lost output")
		}
		return nil
	})
	if err != nil || !observed {
		t.Fatalf("observed=%v err=%v", observed, err)
	}
}
