package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/redis/go-redis/v9"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "work", ParentID: ptr("root"), ActorKind: "operator", ActorKey: "router"}); err != nil {
		t.Fatal(err)
	}
	initial := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	if _, err := store.CreateChatInput(ctx, model.Message{ID: "input", ConversationID: "root", TaskID: "task", Kind: "user", ActorKey: "human", Content: initial}); err != nil {
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
func ptr(value string) *string { return &value }
func outputRequest(key string) loopd.SpeakRequest {
	return loopd.SpeakRequest{Stream: true, Key: key, Actor: loopd.ActorRef{Kind: loopd.RoleHarness, Key: "same-actor"}, Content: json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)}
}
func outputText(t *testing.T, seq uint64, text string) json.RawMessage {
	return marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: seq, Block: map[string]any{"id": "text", "type": "text", "content": text}})
}

// +case=`同一 Actor 的多条消息独立投影、重放；UI 收尾不终止消息写入。`
func TestOutputMessagesOwnIdentityAndReplay(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := store.Speak(ctx, "work", outputRequest("plan"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Speak(ctx, "work", outputRequest("execute"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("messages collided")
	}
	for _, item := range []struct{ id, text string }{{a.ID, "plan"}, {b.ID, "execute"}} {
		event := outputText(t, 2, item.text)
		for i := 0; i < 2; i++ {
			if _, err := producer.EmitMessage(ctx, item.id, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A closed UI stream does not own the lifetime of its writers.
	if _, err := producer.EmitMessage(ctx, a.ID, outputText(t, 3, "later plan")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Speak(ctx, "work", outputRequest("late")); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	err = consumer.Stream(ctx, "task", "root", "", func(v Event) error {
		e, err := agentueui.Parse(v.Data)
		if err != nil {
			return err
		}
		if e.Op == agentueui.OpStart && v.Message != nil {
			var content struct{ Blocks []map[string]any }
			if err := json.Unmarshal(v.Message.Content, &content); err != nil {
				return err
			}
			if len(content.Blocks) > 0 {
				seen[v.MessageID], _ = content.Blocks[0]["content"].(string)
			}
		}
		if e.Op == agentueui.OpSet {
			seen[v.MessageID], _ = e.Block["content"].(string)
		}
		if seen[a.ID] == "later plan" && seen[b.ID] == "execute" {
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) || seen[a.ID] != "later plan" || seen[b.ID] != "execute" {
		t.Fatalf("seen=%v err=%v", seen, err)
	}
	for _, key := range []string{"task", "message/" + a.ID, "message/" + b.ID} {
		if err := producer.events.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := consumer.Stream(ctx, "task", "root", "", func(v Event) error {
		if v.MessageID != "" {
			count++
		}
		if count == 4 {
			return errStop
		}
		return nil
	}); !errors.Is(err, errStop) {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("input and three messages=%d", count)
	}
}
func TestSpeakConcurrentIdentity(t *testing.T) {
	store, _, _ := outputFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < cap(ids); i++ {
		wg.Go(func() {
			m, err := store.Speak(context.Background(), "work", outputRequest("same"))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- m.ID
		})
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first != "" && first != id {
			t.Fatal("duplicate message")
		}
		first = id
	}
	if first == "" {
		t.Fatal("missing message")
	}
	changed := outputRequest("same")
	changed.Target = loopd.ActorRef{Kind: loopd.RoleOperator, Key: "another"}
	if _, err := store.Speak(context.Background(), "work", changed); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("changed recipient=%v", err)
	}
	changed = outputRequest("same")
	changed.Actor.Key = "another"
	other, err := store.Speak(context.Background(), "work", changed)
	if err != nil || other.ID == first {
		t.Fatalf("actor identity scopes speech: %+v %v", other, err)
	}
}
func TestStreamDiscoversOutputDuringDelivery(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created := ""
	observed := false
	err := consumer.Stream(ctx, "task", "root", "", func(v Event) error {
		e, err := agentueui.Parse(v.Data)
		if err != nil {
			return err
		}
		if created == "" && v.MessageID == "" && e.Op == agentueui.OpStart {
			m, err := store.Speak(ctx, "work", outputRequest("later"))
			if err != nil {
				return err
			}
			created = m.ID
			if _, err := producer.EmitMessage(ctx, created, outputText(t, 2, "later")); err != nil {
				return err
			}
			return nil
		}
		if v.MessageID == created {
			observed = true
			return errStop
		}
		if v.MessageID == "" && e.Op == agentueui.OpEnd && !observed {
			t.Fatal("end lost output")
		}
		return nil
	})
	if !errors.Is(err, errStop) || !observed {
		t.Fatalf("observed=%v err=%v", observed, err)
	}
}
