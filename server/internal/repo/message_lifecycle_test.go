package repo

import (
	"bytes"
	"context"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestMessageExpiryPreservesBodyAndRejectsLateOutput(t *testing.T) {
	s := partsStore(t)
	ctx := context.Background()
	m := speech(t, s, 4)
	old := time.Now().UTC().Add(-2 * time.Hour)
	if err := s.db.Model(&model.Message{}).Where("id = ?", m.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	before, err := s.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.ExpireMessages(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil || len(ids) != 1 || ids[0] != m.ID {
		t.Fatalf("expired=%v err=%v", ids, err)
	}
	after, err := s.GetMessage(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "expired" || after.Revision != before.Revision+1 || !after.UpdatedAt.Equal(before.UpdatedAt) || !bytes.Equal(after.Content, before.Content) {
		t.Fatalf("expiry changed content/activity or lost status: before=%+v after=%+v", before, after)
	}
	if err := s.ProjectOutput(ctx, m.ID, ui.End(after.Revision+1)); err == nil {
		t.Fatal("late output resurrected expired message")
	}
	ids, err = s.ExpireMessages(ctx, time.Now(), 100)
	if err != nil || len(ids) != 0 {
		t.Fatalf("terminal expired again: %v %v", ids, err)
	}
}

func TestExpiryDoesNotOverrideConcurrentWrite(t *testing.T) {
	s := partsStore(t)
	m := speech(t, s, 1)
	old := time.Now().Add(-time.Hour)
	if err := s.db.Model(&model.Message{}).Where("id = ?", m.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	// Inject a committed writer after candidate selection, before expiry's CAS.
	callback := "test:refresh_expiry_candidate"
	refreshed := false
	if err := s.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if refreshed {
			return
		}
		refreshed = true
		if err := tx.Session(&gorm.Session{NewDB: true, SkipDefaultTransaction: true}).Model(&model.Message{}).Where("id = ?", m.ID).
			UpdateColumns(map[string]any{"revision": m.Revision + 1, "updated_at": time.Now().UTC()}).Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer s.db.Callback().Update().Remove(callback)
	ids, err := s.ExpireMessages(context.Background(), time.Now().Add(-time.Minute), 100)
	if err != nil || len(ids) != 0 || !refreshed {
		t.Fatalf("stale cleanup won: %v %v", ids, err)
	}
	state, err := s.GetMessageState(context.Background(), m.ID)
	if err != nil || state.Status != contract.MessageStatusStreaming {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestAcceptedOutputRefreshesTTLNotEventTimestamp(t *testing.T) {
	s := partsStore(t)
	m := speech(t, s, 1)
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	event := ui.Event{Op: ui.OpSet, Seq: 2, Timestamp: &future, Block: map[string]any{"id": "b0", "type": "text", "content": "new"}}
	before := time.Now().UTC()
	if err := s.ProjectOutput(context.Background(), m.ID, event); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetMessage(context.Background(), m.ID)
	if err != nil || row.UpdatedAt.Before(before) || row.UpdatedAt.After(time.Now()) {
		t.Fatalf("updated_at follows caller clock: %+v %v", row, err)
	}
	if err := s.ProjectOutput(context.Background(), m.ID, event); err != nil {
		t.Fatal(err)
	}
	retried, err := s.GetMessage(context.Background(), m.ID)
	if err != nil || !retried.UpdatedAt.Equal(row.UpdatedAt) {
		t.Fatal("read/retry refreshed TTL")
	}
}
