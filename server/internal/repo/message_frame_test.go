package repo

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
)

// +case=`One configured byte budget bounds both root and Part content, including JSON envelopes; logical reads remain complete.`
func TestContentMaxBytesAppliesToMessageAndParts(t *testing.T) {
	const budget = 1024
	s, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "content.db"), ContentMaxBytes: budget})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	m := speech(t, s, 1)
	text := strings.Repeat("界", 2000)
	if err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b0", "content": text}}); err != nil {
		t.Fatal(err)
	}
	root, err := storedMessage(s, ctx, m.ID)
	if err != nil || len(root.Content) > budget {
		t.Fatalf("root bytes=%d err=%v", len(root.Content), err)
	}
	parts := storedParts(t, s, m.ID)
	if len(parts) < 2 {
		t.Fatal("configured budget did not split large block")
	}
	for _, part := range parts {
		if len(part.Content) > budget {
			t.Fatalf("part bytes=%d exceeds budget", len(part.Content))
		}
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || snapshotOf(t, got.Content)["blocks"].([]any)[0].(map[string]any)["content"] != "hello"+text {
		t.Fatalf("logical read failed: %v", err)
	}
	content := func(meta string) []byte {
		raw, _ := json.Marshal(map[string]any{"version": "1.1", "biz": "chat", "meta": map[string]any{"text": meta}, "blocks": []any{}})
		return raw
	}
	raw := content(strings.Repeat("x", budget-len(content(""))))
	if _, err := s.CreateMessage(ctx, model.Message{ID: "boundary", ConversationID: "conv", SourceKind: "user", Content: raw}); err != nil {
		t.Fatal(err)
	}
	raw = content(strings.Repeat("x", budget-len(content(""))+1))
	if _, err := s.UpdateMessageContent(ctx, "conv", "boundary", raw); !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("oversized root error=%v", err)
	}
}

// +case=`Large logical blocks round-trip through <=64 KiB physical rows; retries, replacement, rollback and deletion never retain mixed frame revisions.`
func TestMessageFramesLifecycle(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	m := speech(t, s, 1)
	text := strings.Repeat("你好🙂\"\\\n<>&", 30000)
	event := ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b0", "content": text}}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	parts := storedParts(t, s, m.ID)
	if len(parts) < 2 {
		t.Fatal("large block did not create frames")
	}
	for _, part := range parts {
		if len(part.Content) > 64<<10 || part.SizeBytes != len(part.Content) || !json.Valid(part.Content) {
			t.Fatalf("invalid physical part %s size=%d", part.ID, len(part.Content))
		}
	}
	root, err := storedMessage(s, ctx, m.ID)
	if err != nil || len(root.Content) > 64<<10 {
		t.Fatalf("root size=%d err=%v", len(root.Content), err)
	}
	page, err := s.MessageBlocks(ctx, "conv", m.ID, "b0", "")
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("block read: %v", err)
	}
	var block struct{ Content string }
	if err := json.Unmarshal(page.Data[0], &block); err != nil || block.Content != "hello"+text {
		t.Fatalf("logical content not preserved: %v", err)
	}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parts, storedParts(t, s, m.ID)) {
		t.Fatal("retry replaced already committed frames")
	}
	if err := s.db.Exec("CREATE TRIGGER reject_frame_root BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	event.Seq = 3
	event.Block["content"] = " more"
	if err := s.ProjectOutput(ctx, m.ID, event); err == nil {
		t.Fatal("expected root write failure")
	}
	after, _ := storedMessage(s, ctx, m.ID)
	if !reflect.DeepEqual(root, after) || !reflect.DeepEqual(parts, storedParts(t, s, m.ID)) {
		t.Fatal("failed transaction changed root or frames")
	}
	if err := s.db.Exec("DROP TRIGGER reject_frame_root").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectOutput(ctx, m.ID, event); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || snapshotOf(t, got.Content)["blocks"].([]any)[0].(map[string]any)["content"] != "hello"+text+" more" {
		t.Fatalf("updated frame read: %v", err)
	}
	if _, err := s.UpdateMessageContent(ctx, "conv", m.ID, blocksContent(t, 1)); err != nil {
		t.Fatal(err)
	}
	if len(storedParts(t, s, m.ID)) != 1 {
		t.Fatal("replacement retained obsolete frames")
	}
	if err := s.DeleteMessage(ctx, m.ID); err != nil || len(storedParts(t, s, m.ID)) != 0 {
		t.Fatalf("delete did not clean parts: %v", err)
	}
}

func TestMessageFramesMissingPartFailsReads(t *testing.T) {
	s := humanStore(t)
	m := speech(t, s, 1)
	ctx := context.Background()
	if err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpAppend, Seq: 2, Mask: "block.content", Block: map[string]any{"id": "b0", "content": strings.Repeat("x", 128<<10)}}); err != nil {
		t.Fatal(err)
	}
	parts := storedParts(t, s, m.ID)
	if err := s.db.Delete(&model.MessagePart{}, "id = ?", parts[len(parts)-1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMessage(ctx, m.ID); err == nil {
		t.Fatal("snapshot accepted incomplete frame group")
	}
	if _, err := s.MessageBlocks(ctx, "conv", m.ID, "b0", ""); err == nil {
		t.Fatal("block read accepted incomplete frame group")
	}
}

func TestMessageRootWriteLimit(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	content := func(meta string) []byte {
		raw, _ := json.Marshal(map[string]any{"version": "1.1", "biz": "chat", "meta": map[string]any{"text": meta}, "blocks": []any{}})
		return raw
	}
	for _, delta := range []int{0, 1} {
		raw := content(strings.Repeat("x", (64<<10)-len(content(""))+delta))
		_, err := s.CreateMessage(ctx, model.Message{ID: "boundary", ConversationID: "conv", SourceKind: "user", Content: raw})
		if delta == 0 {
			if err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteMessage(ctx, "boundary"); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrContentTooLarge) {
			t.Fatalf("oversized root error=%v", err)
		}
	}
	if _, err := s.GetMessage(ctx, "boundary"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed create left a message: %v", err)
	}
	m := speech(t, s, 1)
	before, _ := storedMessage(s, ctx, m.ID)
	err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpSet, Seq: 2, Mask: "meta.large", Meta: map[string]any{"large": strings.Repeat("x", 64<<10)}})
	if !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("oversized metadata=%v", err)
	}
	after, _ := storedMessage(s, ctx, m.ID)
	if !reflect.DeepEqual(before, after) || len(storedParts(t, s, m.ID)) != 0 {
		t.Fatal("failed metadata update mutated storage")
	}
	// Caller-supplied frames cannot masquerade as resolved logical input.
	_, err = s.Speak(ctx, "conv", contract.SpeakRequest{Key: "forged", Actor: contract.ActorRef{Kind: "user", Key: "u"}, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[{"id":"f","type":"frame"}]}`)})
	if !errors.Is(err, ErrInvalidContent) {
		t.Fatalf("accepted external frame: %v", err)
	}
	// Frame contents are bounded, but the root reference directory must fit too.
	_, err = s.CreateMessage(ctx, model.Message{ID: "directory", ConversationID: "conv", SourceKind: "user", Content: blocksContent(t, 2000)})
	if !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("oversized reference directory=%v", err)
	}
	if _, err := s.GetMessage(ctx, "directory"); !errors.Is(err, ErrNotFound) || len(storedParts(t, s, "directory")) != 0 {
		t.Fatalf("failed directory write left data: %v", err)
	}
}
