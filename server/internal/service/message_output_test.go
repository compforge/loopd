package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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

func TestMessageOutputAcrossInstances(t *testing.T) {
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
	producer := NewMessageService(store, agentuerunner.NewRedisEventBridge(clientA, options), nil)
	consumer := NewMessageService(store, agentuerunner.NewRedisEventBridge(clientB, options), nil)

	if _, err := store.CreateMessage(ctx, model.Message{Status: "streaming", ID: "message-2", ConversationID: "conversation-1", TaskID: "task-1", Kind: "operator", ActorKey: "intent", Content: initial, Revision: 1}); err != nil {
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

	seen, completed := false, false
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
			if !completed || event.Message != nil {
				t.Fatal("missing terminal status")
			}
			return errStop
		}
		if patch.Op == agentueui.OpStart && event.Message.Status == contract.MessageStatusCompleted {
			completed = true
		}
		return nil
	}); !errors.Is(err, errStop) {
		t.Fatal(err)
	}

}

// +case=`A long-message snapshot is sent once; Redis deltas only carry an addressed patch, then a SQL terminal snapshot.`
func TestConversationDeltaDoesNotRepeatMessage(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := store.Speak(ctx, "work", outputRequest("long-output"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("long body ", 10000)
	if _, err := producer.EmitMessage(ctx, m.ID, outputText(t, 2, body)); err != nil {
		t.Fatal(err)
	}
	snapshotSeen, deltaSeen, completed := false, false, false
	err = listen(ctx, consumer, "work", func(value Event) error {
		event, err := agentueui.Parse(value.Data)
		if err != nil {
			return err
		}
		if event.Op == agentueui.OpPing {
			if event.StreamID != "" {
				t.Fatal("connection ping addresses a message")
			}
			return nil
		}
		if event.StreamID != m.ID {
			t.Fatalf("stream identity = %q", event.StreamID)
		}
		switch event.Op {
		case agentueui.OpStart:
			if value.Message == nil || value.Message.Revision != event.Seq {
				t.Fatal("snapshot lacks matching message metadata")
			}
			if !snapshotSeen {
				snapshotSeen = true
				if !strings.Contains(string(value.Message.Content), body) {
					t.Fatal("missing initial body")
				}
				_, err = producer.EmitMessage(ctx, m.ID, marshalEvent(t, agentueui.Event{
					Op: agentueui.OpAppend, Seq: 3, Mask: "block.content",
					Block: map[string]any{"id": "text", "type": "text", "content": "!"},
				}))
				return err
			}
			completed = value.Message.Status == contract.MessageStatusCompleted
		case agentueui.OpAppend:
			if value.Message != nil || len(value.Data) > 512 || event.Seq != 3 {
				t.Fatalf("delta repeats snapshot or has wrong revision: %d bytes", len(value.Data))
			}
			deltaSeen = true
			_, err = producer.EmitMessage(ctx, m.ID, marshalEvent(t, agentueui.End(4)))
			return err
		case agentueui.OpEnd:
			if value.Message != nil || !completed {
				t.Fatal("End must follow terminal metadata without repeating it")
			}
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) || !snapshotSeen || !deltaSeen || !completed {
		t.Fatalf("snapshot=%v delta=%v completed=%v err=%v", snapshotSeen, deltaSeen, completed, err)
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
	coordinator := NewMessageService(store, agentuerunner.NewRedisEventBridge(client, agentuerunner.BridgeOptions{ReadBlock: time.Millisecond}), nil)
	// No Redis stream exists. Observe must recover Human snapshots from Messages.
	seen := map[string]bool{}
	err = listen(ctx, coordinator, "conv", func(value Event) error {
		event, err := agentueui.Parse(value.Data)
		if err != nil {
			return err
		}
		if event.Op == agentueui.OpStart && value.Message == nil {
			t.Fatal("snapshot missing Message envelope")
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
		if value.Message != nil && value.Message.IsHumanReply() {
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
