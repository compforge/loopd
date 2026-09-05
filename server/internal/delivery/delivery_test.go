package delivery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/redis/go-redis/v9"
)

func TestCoordinatorCompletesAndStreamsAcrossInstances(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conversation-1"}); err != nil {
		t.Fatal(err)
	}
	initial := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateChatMessages(ctx,
		model.Message{ID: "message-1", ConversationID: "conversation-1", TaskID: "task-1", Kind: "user", SenderKey: "user-1", Content: initial},
		model.Message{ID: "message-2", ConversationID: "conversation-1", TaskID: "task-1", Kind: "operator", SenderKey: "intent", Content: initial},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	redisServer := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = clientA.Close() })
	t.Cleanup(func() { _ = clientB.Close() })
	options := agentuerunner.BridgeOptions{KeyPrefix: "test", ReadBlock: time.Millisecond}
	producer := New(agentuerunner.NewRedisEventBridge(clientA, options), store, nil)
	consumer := New(agentuerunner.NewRedisEventBridge(clientB, options), store, nil)

	if err := producer.Initialize(ctx, "task-1", initial); err != nil {
		t.Fatal(err)
	}
	set := marshalEvent(t, agentueui.Event{
		Op: agentueui.OpSet, Seq: 2,
		Block: map[string]any{"id": "answer", "type": "text", "content": "hello"},
	})
	cursor, err := producer.Emit(ctx, "task-1", set)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent := marshalEvent(t, agentueui.Event{
		Op: agentueui.OpAppend, Seq: 3, Mask: "block.content",
		Block: map[string]any{"id": "answer", "type": "text", "content": " world"},
	})
	if _, err := producer.Emit(ctx, "task-1", appendEvent); err != nil {
		t.Fatal(err)
	}
	if err := producer.Complete(ctx, "task-1", nil); err != nil {
		t.Fatal(err)
	}

	message, err := store.GetMessage(ctx, "message-2")
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(message.Content, &snapshot); err != nil {
		t.Fatal(err)
	}
	blocks := snapshot["blocks"].([]any)
	if blocks[0].(map[string]any)["content"] != "hello world" {
		t.Fatalf("persisted snapshot = %s", message.Content)
	}

	var delivered []Event
	if err := consumer.Stream(ctx, "task-1", "conversation-1", cursor, func(event Event) error {
		delivered = append(delivered, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 3 {
		t.Fatalf("got %d deliveries, want reconstructed start, append, and end", len(delivered))
	}
	first, err := agentueui.Parse(delivered[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if first.Op != agentueui.OpStart || first.Seq != 2 || delivered[0].Persisted {
		t.Fatalf("first delivery = %#v, parsed=%#v", delivered[0], first)
	}
	last, err := agentueui.Parse(delivered[2].Data)
	if err != nil {
		t.Fatal(err)
	}
	if last.Op != agentueui.OpEnd || !delivered[2].Persisted {
		t.Fatalf("last delivery = %#v, parsed=%#v", delivered[2], last)
	}
}

func marshalEvent(t *testing.T, event agentueui.Event) json.RawMessage {
	t.Helper()
	data, err := event.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
