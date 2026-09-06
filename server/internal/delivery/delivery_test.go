package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/redis/go-redis/v9"
)

func TestCoordinatorCompletesAndStreamsAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "loopd.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conversation-1"}); err != nil {
		t.Fatal(err)
	}
	initial := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateChatInput(ctx,
		model.Message{ID: "message-1", ConversationID: "conversation-1", TaskID: "task-1", Kind: "user", ActorKey: "user-1", Content: initial},
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

	if _, err := store.CreateMessage(ctx, model.Message{Status: "streaming", ID: "message-2", ConversationID: "conversation-1", TaskID: "task-1", Kind: "operator", ActorKey: "intent", Purpose: "output", Content: initial, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	set := marshalEvent(t, agentueui.Event{
		Op: agentueui.OpSet, Seq: 2,
		Block: map[string]any{"id": "answer", "type": "text", "content": "hello"},
	})
	_, err = producer.EmitMessage(ctx, "message-2", set)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent := marshalEvent(t, agentueui.Event{
		Op: agentueui.OpAppend, Seq: 3, Mask: "block.content",
		Block: map[string]any{"id": "answer", "type": "text", "content": " world"},
	})
	if _, err := producer.EmitMessage(ctx, "message-2", appendEvent); err != nil {
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

	seen := false
	if err := listen(ctx, consumer, "conversation-1", func(event Event) error {
		if event.MessageID != "message-2" {
			return nil
		}
		patch, err := agentueui.Parse(event.Data)
		if err != nil {
			return err
		}
		if patch.Op == agentueui.OpStart && !seen {
			seen = true
			_, err = producer.EmitMessage(ctx, "message-2", marshalEvent(t, agentueui.End(4)))
			return err
		}
		if patch.Op == agentueui.OpEnd {
			if event.Message.Status != contract.MessageStatusCompleted {
				t.Fatal("missing terminal status")
			}
			return errStop
		}
		return nil
	}); !errors.Is(err, errStop) {
		t.Fatal(err)
	}

}

var errStop = errors.New("page unsubscribed")

func marshalEvent(t *testing.T, event agentueui.Event) json.RawMessage {
	t.Helper()
	data, err := event.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// +case=`独立问题快照在主流结束前交付；相同 block ID 不折叠到主回答`
func TestHumanSnapshotsAreMessageAddressedAndRecoverWithoutRedis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "human.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	initial := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateChatInput(ctx, model.Message{ID: "00000000-0000-7000-8000-000000000001", ConversationID: "conv", TaskID: "task", Kind: "user", ActorKey: "alice", Content: initial})
	if err != nil {
		t.Fatal(err)
	}
	r := contract.HumanRequest{ConversationID: "conv", Actor: contract.ActorRef{Kind: contract.ActorKindOperator, Key: "router"}, Target: contract.ActorRef{Kind: contract.ActorKindUser, Key: "alice"}, ReplyToID: "00000000-0000-7000-8000-000000000001", EffectKey: "ask", Type: "ask", Title: "Question", Prompt: "Reply", Timeout: time.Minute, AllowOther: true}
	q, err := store.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	coordinator := New(agentuerunner.NewRedisEventBridge(client, agentuerunner.BridgeOptions{ReadBlock: time.Millisecond}), store, nil)
	// No Redis stream exists. Observe must recover Human snapshots from Messages.
	seen := map[string]bool{}
	err = listen(ctx, coordinator, "conv", func(value Event) error {
		if value.MessageID != "" && value.Message == nil {
			t.Fatal("missing Message envelope")
		}
		event, err := agentueui.Parse(value.Data)
		if err != nil {
			return err
		}
		if value.MessageID == q.Message.ID && event.Op == agentueui.OpStart {
			if !seen["question"] {
				seen["question"] = true
				_, err = store.ReplyHuman(ctx, "conv", "alice", contract.HumanReply{ReplyToID: q.Message.ID, Outcome: contract.HumanSuccess, Value: "answer"})
				if err != nil {
					return err
				}
			}
		}
		if value.Message != nil && value.Message.Purpose == "human_reply" {
			seen["reply"] = true
		}
		if seen["question"] && seen["reply"] {
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("snapshots=%v err=%v", seen, err)
	}
	rows, err := store.ListMessages(ctx, "conv", "", 100)
	if err != nil || len(rows) != 3 {
		t.Fatalf("expected input, question, reply: %+v %v", rows, err)
	}
}
