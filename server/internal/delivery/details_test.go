package delivery

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/redis/go-redis/v9"
)

func TestDetailMessagesSurviveCompletionAndReplay(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "detail.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initial := json.RawMessage(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateConversation(ctx, model.Conversation{ID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateChatMessages(ctx,
		model.Message{ID: "user", ConversationID: "root", TaskID: "task", Kind: "user", ActorKey: "human", Content: initial},
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

	first := marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 2, Block: map[string]any{
		"id": "call-a/text", "type": "text", "content": "plan", "call_id": "call-a", "effect_key": "plan",
	}})
	for i := 0; i < 2; i++ {
		if _, err := producer.Emit(ctx, "task", first); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := store.FindConversationByParentMessage(ctx, "response")
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.ListMessages(ctx, detail.ID, "", 100)
	if err != nil || len(live) != 1 || live[0].ActorKey != "call-a" {
		t.Fatalf("live detail=%+v err=%v", live, err)
	}
	liveID := live[0].ID
	if _, err := consumer.Emit(ctx, "task", marshalEvent(t, agentueui.Event{Op: agentueui.OpAppend, Seq: 3, Mask: "block.content", Block: map[string]any{
		"id": "call-a/text", "type": "text", "content": " done", "call_id": "call-a", "effect_key": "plan",
	}})); err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.Emit(ctx, "task", marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 4, Block: map[string]any{
		"id": "call-a/tool", "type": "tool", "name": "search", "call_id": "call-a", "effect_key": "plan",
	}})); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Emit(ctx, "task", marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 5, Block: map[string]any{
		"id": "call-b/text", "type": "text", "content": "result", "call_id": "call-b", "effect_key": "work/0",
	}})); err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.Emit(ctx, "task", marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 6, Block: map[string]any{
		"id": "answer", "type": "text", "content": "summary",
	}})); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Complete(ctx, "task", nil); err != nil {
		t.Fatal(err)
	}
	if err := producer.Complete(ctx, "task", nil); err != nil {
		t.Fatal(err)
	}
	// Rejected late events must not create empty detail records.
	_, err = producer.Emit(ctx, "task", marshalEvent(t, agentueui.Event{Op: agentueui.OpSet, Seq: 100, Block: map[string]any{
		"id": "late/text", "type": "text", "content": "late", "call_id": "late-call",
	}}))
	if err == nil {
		t.Fatal("accepted an event after completion")
	}
	rootMessages, err := store.ListMessages(ctx, "root", "", 100)
	if err != nil || len(rootMessages) != 2 {
		t.Fatalf("root messages=%+v err=%v", rootMessages, err)
	}
	var root struct {
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(rootMessages[0].Content, &root); err != nil {
		t.Fatal(err)
	}
	// IDs in this fixture sort response before user.
	if len(root.Blocks) != 1 || root.Blocks[0]["content"] != "summary" {
		t.Fatalf("root content=%s", rootMessages[0].Content)
	}
	details, err := store.ListMessages(ctx, detail.ID, "", 100)
	if err != nil || len(details) != 2 || details[0].ID != liveID {
		t.Fatalf("details=%+v err=%v", details, err)
	}
	var saved struct {
		Meta   map[string]any   `json:"meta"`
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(details[0].Content, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Meta["effect_key"] != "plan" || len(saved.Blocks) != 2 || saved.Blocks[0]["content"] != "plan done" {
		t.Fatalf("saved detail=%s", details[0].Content)
	}
	roots, err := store.ListConversations(ctx, "", 100)
	if err != nil || len(roots) != 1 || roots[0].ID != "root" {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
	contextMessages, err := store.ListRootMessagesByTask(ctx, "task")
	if err != nil || len(contextMessages) != 2 {
		t.Fatalf("task context messages=%+v err=%v", contextMessages, err)
	}
	var replayed int
	if err := producer.Stream(ctx, "task", "root", "", func(event Event) error { replayed++; return nil }); err != nil {
		t.Fatal(err)
	}
	if replayed == 0 {
		t.Fatal("missing task replay")
	}
}

func TestEnsureDetailMessageConcurrentIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := repo.Open(repo.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "parallel.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initial := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	_, err = store.CreateConversation(ctx, model.Conversation{ID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateMessage(ctx, model.Message{ID: "parent", ConversationID: "root", TaskID: "task", Kind: "operator", ActorKey: "router", Content: initial})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := New(nil, store, nil)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < cap(ids); i++ {
		wg.Go(func() {
			message, err := coordinator.ensureDetail(ctx, parent, "same-call", map[string]any{"effect_key": "work/0"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- message.ID
		})
	}
	wg.Wait()
	close(ids)
	var firstID string
	for id := range ids {
		if firstID != "" && firstID != id {
			t.Fatalf("duplicate message identities %s and %s", firstID, id)
		}
		firstID = id
	}
	if firstID == "" {
		t.Fatal("no detail Message created")
	}
}

func TestDirectHarnessOutputStaysInMainMessage(t *testing.T) {
	snapshot := map[string]any{"version": "1.0", "biz": "chat", "meta": map[string]any{}, "blocks": []any{
		map[string]any{"id": "text", "type": "text", "content": "direct answer", "call_id": "direct-call"},
	}}
	// A direct Harness response must not consult the detail repository at all.
	result, err := New(nil, nil, nil).persistDetails(context.Background(), model.Message{Kind: "harness"}, snapshot)
	if err != nil || len(result["blocks"].([]any)) != 1 {
		t.Fatalf("direct output=%+v err=%v", result, err)
	}
}
