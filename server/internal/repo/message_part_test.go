package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

func partsStore(t *testing.T) *Store {
	t.Helper()
	s := humanStore(t)
	s.messageInlineBlocks = 1
	s.messageInlineBytes = 128
	s.messagePartBytes = 120
	return s
}
func blocksContent(t *testing.T, n int) []byte {
	t.Helper()
	blocks := []any{}
	for i := 0; i < n; i++ {
		blocks = append(blocks, map[string]any{"id": fmt.Sprintf("b%d", i), "type": "text", "content": "hello"})
	}
	data, err := json.Marshal(map[string]any{"version": "1.0", "biz": "chat", "meta": map[string]any{}, "blocks": blocks})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func storedParts(t *testing.T, s *Store, id string) []model.MessagePart {
	t.Helper()
	var parts []model.MessagePart
	if err := s.db.Where("message_id = ?", id).Order("id ASC").Find(&parts).Error; err != nil {
		t.Fatal(err)
	}
	return parts
}
func snapshotOf(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func storedMessage(s *Store, ctx context.Context, id string) (model.Message, error) {
	var m model.Message
	err := s.db.WithContext(ctx).First(&m, "id = ?", id).Error
	return m, err
}
func refAt(t *testing.T, s *Store, id string, index int) string {
	t.Helper()
	m, err := storedMessage(s, context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	b := snapshotOf(t, m.Content)["blocks"].([]any)[index].(map[string]any)
	ref, _ := b["ref"].(string)
	if ref != "" && len(b) != 2 {
		t.Fatalf("mixed reference/body: %#v", b)
	}
	return ref
}
func speech(t *testing.T, s *Store, n int) model.Message {
	t.Helper()
	m, err := s.Speak(context.Background(), "conv", loopd.SpeakRequest{Key: "speech", Stream: true, Actor: loopd.ActorRef{Kind: loopd.ActorKindHarness, Key: "writer"}, Target: loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "reader"}, Content: blocksContent(t, n)})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func assertExpanded(t *testing.T, m model.Message, n int) {
	t.Helper()
	snapshot := snapshotOf(t, m.Content)
	blocks := snapshot["blocks"].([]any)
	if len(blocks) != n {
		t.Fatalf("blocks=%d want=%d", len(blocks), n)
	}
	for _, v := range blocks {
		if _, ref := v.(map[string]any)["ref"]; ref {
			t.Fatalf("reference escaped storage: %s", m.Content)
		}
	}
}

// +case=`很多 block 混合内联和引用，读取和 Speak 重试返回完整正文；相同 block ID 不跨 Message 合并`
func TestMessagePartsMixedStorageAndReadPaths(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 4)
	assertExpanded(t, m, 4)
	if refAt(t, s, m.ID, 0) != "" || refAt(t, s, m.ID, 1) == "" || refAt(t, s, m.ID, 1) != refAt(t, s, m.ID, 2) {
		t.Fatal("expected inline prefix and packed reference blocks")
	}
	if len(storedParts(t, s, m.ID)) != 2 {
		t.Fatal("expected two parts")
	}
	again := speech(t, s, 4)
	if again.ID != m.ID {
		t.Fatal("Speak lost identity")
	}
	assertExpanded(t, again, 4)
	inbox, err := s.ListInbox(ctx, "conv", "operator", "reader", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range inbox {
		if row.ID == m.ID {
			t.Fatal("unfinished output delivered")
		}
	}
	if err := s.ProjectOutput(ctx, m.ID, ui.End(2)); err != nil {
		t.Fatal(err)
	}
	reads := []func() ([]model.Message, error){
		func() ([]model.Message, error) { return s.ListMessages(ctx, "conv", "", 100) },
		func() ([]model.Message, error) { return s.ListInbox(ctx, "conv", "operator", "reader", "", 100) },
		func() ([]model.Message, error) { return s.ListDeliveryMessages(ctx, "conv") },
		func() ([]model.Message, error) { return s.PendingDispatches(ctx, 100) },
	}
	for _, read := range reads {
		rows, err := read()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.ID == m.ID {
				found = true
				assertExpanded(t, row, 4)
			}
		}
		if !found {
			t.Fatal("message absent from read path")
		}
	}
	input, err := s.CreateChatInput(ctx, model.Message{ID: "input-parts", ConversationID: "conv", TaskID: "parts-task", Kind: "user", ActorKey: "alice", Content: blocksContent(t, 4)})
	if err != nil {
		t.Fatal(err)
	}
	assertExpanded(t, input, 4)
	delivery, err := s.GetDeliveryInput(ctx, "parts-task")
	if err != nil {
		t.Fatal(err)
	}
	assertExpanded(t, delivery, 4)
	roots, err := s.ListRootMessagesByTask(ctx, "parts-task")
	if err != nil || len(roots) != 1 {
		t.Fatalf("roots=%v %v", roots, err)
	}
	assertExpanded(t, roots[0], 4)
	if refAt(t, s, input.ID, 1) == refAt(t, s, m.ID, 1) {
		t.Fatal("different messages share physical parts")
	}
}

// +case=`旧 block 增长时迁移 Part，重试不重复 append，不读取或重写其它 Part`
func TestMessagePartsLatePatchMovesOnlyAffectedBlock(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 4)
	old := refAt(t, s, m.ID, 1)
	untouched := refAt(t, s, m.ID, 3)
	before := storedParts(t, s, m.ID)
	queries := 0
	if err := s.db.Callback().Query().Before("gorm:query").Register("count_part_reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "message_parts" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	event := ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b1", "content": strings.Repeat("界", 200)}}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("part reads=%d want=1", queries)
	}
	queries = 0
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	if queries != 0 {
		t.Fatalf("retry read parts: %d", queries)
	}
	if err := s.db.Callback().Query().Remove("count_part_reads"); err != nil {
		t.Fatal(err)
	}
	if refAt(t, s, m.ID, 1) == old || refAt(t, s, m.ID, 2) != old {
		t.Fatal("wrong block moved")
	}
	after := storedParts(t, s, m.ID)
	for _, p := range before {
		if p.ID == untouched {
			for _, q := range after {
				if p.ID == q.ID && !reflect.DeepEqual(p, q) {
					t.Fatal("unrelated part changed")
				}
			}
		}
	}
	actual, err := s.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	blocks := snapshotOf(t, actual.Content)["blocks"].([]any)
	if got := blocks[1].(map[string]any)["content"]; got != "hello"+strings.Repeat("界", 200) {
		t.Fatalf("duplicated or lost append: %v", got)
	}
	for i, v := range blocks {
		if v.(map[string]any)["id"] != fmt.Sprintf("b%d", i) {
			t.Fatal("block order changed")
		}
	}
	event.Block["content"] = "different"
	if err := s.ProjectOutput(ctx, m.ID, event); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	if err := s.ProjectOutput(ctx, m.ID, ui.End(3)); err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpSet, Seq: 4, Block: map[string]any{"id": "b1", "type": "text", "content": "late"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("write after End=%v", err)
	}
}

func TestMessagePartsInlineGrowthAndSingleLargeBlock(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 1)
	if len(storedParts(t, s, m.ID)) != 0 {
		t.Fatal("small message allocated part")
	}
	event := ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b0", "content": strings.Repeat("界", 100)}}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	parts := storedParts(t, s, m.ID)
	if len(parts) != 1 || parts[0].SizeBytes <= s.messagePartBytes || parts[0].SizeBytes != len(parts[0].Content) {
		t.Fatalf("large block packing=%+v", parts)
	}
	key := refAt(t, s, m.ID, 0)
	if err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpSet, Seq: 3, Block: map[string]any{"id": "b0", "type": "text", "content": "small again"}}); err != nil {
		t.Fatal(err)
	}
	if refAt(t, s, m.ID, 0) != key {
		t.Fatal("external block moved back inline")
	}
}

func TestMessagePartsRollbackReplacementAndDelete(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 4)
	before, err := storedMessage(s, ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := storedParts(t, s, m.ID)
	if err := s.db.Exec("CREATE TRIGGER reject_header BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'injected header failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	event := ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b1", "content": strings.Repeat("x", 500)}}
	if err := s.ProjectOutput(ctx, m.ID, event); err == nil {
		t.Fatal("expected transaction failure")
	}
	after, err := storedMessage(s, ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(parts, storedParts(t, s, m.ID)) {
		t.Fatal("failed transaction left partial storage")
	}
	if err := s.db.Exec("DROP TRIGGER reject_header").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	replaced, err := s.UpdateMessageContent(ctx, "conv", m.ID, blocksContent(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	assertExpanded(t, replaced, 2)
	total := 0
	for _, p := range storedParts(t, s, m.ID) {
		decoded, err := decodePart(p)
		if err != nil {
			t.Fatal(err)
		}
		total += len(decoded.body.Blocks)
	}
	if total != 1 {
		t.Fatalf("obsolete bodies retained: %d", total)
	}
	if err := s.DeleteMessage(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if len(storedParts(t, s, m.ID)) != 0 {
		t.Fatal("message deletion left parts")
	}
	if _, err := s.GetMessage(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("message still exists: %v", err)
	}
}

func TestMessagePartsRejectCrossMessageAndMissingReferences(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 3)
	other, err := s.CreateMessage(ctx, model.Message{ID: "other", ConversationID: "conv", Kind: "operator", ActorKey: "other", Content: blocksContent(t, 3)})
	if err != nil {
		t.Fatal(err)
	}
	foreign := refAt(t, s, other.ID, 1)
	for _, key := range []string{foreign, "missing"} {
		header, err := storedMessage(s, ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		snap := snapshotOf(t, header.Content)
		snap["blocks"].([]any)[1] = map[string]any{"id": "b1", "ref": key}
		data, _ := json.Marshal(snap)
		if err := s.db.Model(&header).UpdateColumn("content", data).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetMessage(ctx, m.ID); err == nil {
			t.Fatal("resolved foreign or missing part")
		}
	}
}

func TestMessagePartsHumanReplyTimeoutAndRecovery(t *testing.T) {
	s := partsStore(t)
	s.messageInlineBytes = 1
	ctx := context.Background()
	q, err := s.CreateHuman(ctx, question("external"))
	if err != nil {
		t.Fatal(err)
	}
	if refAt(t, s, q.Message.ID, 0) == "" {
		t.Fatal("question not externalized")
	}
	answer, err := s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Status != loopd.HumanSuccess || answer.Value != "small" {
		t.Fatalf("reply=%+v", answer)
	}
	again, err := s.CreateHuman(ctx, question("external"))
	if err != nil || again.Value != "small" || again.Reply.ID != answer.Reply.ID {
		t.Fatalf("recovery=%+v %v", again, err)
	}
	expired := question("timeout-external")
	expired.Timeout = time.Nanosecond
	q, err = s.CreateHuman(ctx, expired)
	if err != nil {
		t.Fatal(err)
	}
	timed, err := s.GetHuman(ctx, q.Message.ID)
	if err != nil || timed.Status != loopd.HumanTimeout || timed.Reply != nil {
		t.Fatalf("timeout=%+v %v", timed, err)
	}
}

func TestMessagePartsConcurrentRetryAndRead(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 4)
	event := ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b1", "content": strings.Repeat("x", 500)}}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 6; i++ {
		wg.Go(func() { errs <- s.ProjectOutput(ctx, m.ID, event) })
		wg.Go(func() {
			got, err := s.GetMessage(ctx, m.ID)
			if err == nil {
				b := snapshotOf(t, got.Content)["blocks"].([]any)[1].(map[string]any)
				want := "hello"
				if got.Revision == 2 {
					want += strings.Repeat("x", 500)
				}
				if b["content"] != want {
					err = fmt.Errorf("mixed revision/body")
				}
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMessagePartsCreateFailureAndMissingBlock(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	if err := s.db.Exec("CREATE TRIGGER reject_part BEFORE INSERT ON message_parts BEGIN SELECT RAISE(ABORT, 'injected part failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateMessage(ctx, model.Message{ID: "failed", ConversationID: "conv", Kind: "operator", ActorKey: "writer", Content: blocksContent(t, 3)})
	if err == nil {
		t.Fatal("expected part creation failure")
	}
	if _, err := s.GetMessage(ctx, "failed"); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed create left message")
	}
	if len(storedParts(t, s, "failed")) != 0 {
		t.Fatal("failed create left parts")
	}
	if err := s.db.Exec("DROP TRIGGER reject_part").Error; err != nil {
		t.Fatal(err)
	}
	m := speech(t, s, 3)
	key := refAt(t, s, m.ID, 1)
	if err := s.db.Model(&model.MessagePart{}).Where("id = ?", key).UpdateColumn("content", []byte(`{"blocks":[{"id":"wrong","type":"text","content":"foreign body"}]}`)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, m.ID); err == nil {
		t.Fatal("part with wrong block ID resolved")
	}
}

func TestMessagePartsPreserveNumbersWhenMovingContent(t *testing.T) {
	s := partsStore(t)
	s.messageInlineBytes = 1
	ctx := context.Background()
	content := []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"b","type":"tool","result":{"integer":9007199254740993,"decimal":0.1234567890123456789012345}}]}`)
	m, err := s.CreateMessage(ctx, model.Message{ID: "numbers", ConversationID: "conv", Kind: "operator", ActorKey: "writer", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []string{"9007199254740993", "0.1234567890123456789012345"} {
		if !strings.Contains(string(loaded.Content), number) {
			t.Fatalf("lost numeric precision: %s", loaded.Content)
		}
	}
}
