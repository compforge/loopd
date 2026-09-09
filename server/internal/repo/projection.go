package repo

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/compforge/agentue/sdks/go/storage"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectOutput maintains the visible snapshot for conversation-scoped output
// before attempting page delivery. SQL serializes revisions and remembers the
// last event fingerprint so an ambiguous response can be retried without appending twice.
func (s *Store) ProjectOutput(ctx context.Context, id string, event agentueui.Event, statuses ...contract.MessageStatus) error {
	status := contract.MessageStatus("")
	if len(statuses) > 1 {
		return ErrConflict
	}
	if len(statuses) == 1 {
		status = statuses[0]
	}
	if event.Op == agentueui.OpEnd && status == "" {
		status = contract.MessageStatusCompleted
	}
	if (event.Op == agentueui.OpEnd && !status.Terminal()) || (event.Op != agentueui.OpEnd && status != "") {
		return ErrConflict
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.projectOutput(tx, id, event, status, false) })
}
func (s *Store) projectOutput(tx *gorm.DB, id string, event agentueui.Event, status contract.MessageStatus, harnessWrite bool) error {
	data, err := event.Marshal()
	if err != nil {
		return err
	}
	// Status is part of the write identity, outside the AgentUE event payload.
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append(data, []byte("\x00"+string(status))...)))

	var m model.Message
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "id = ?", id).Error; err != nil {
		return err
	}
	if !harnessWrite {
		if err := requireUnownedMessage(tx, m.ID); err != nil {
			return err
		}
	}
	c, err := openMessageContent(tx, m)
	if err != nil {
		return err
	}
	snapshot := c.snapshot
	meta, _ := snapshot["meta"].(map[string]any)
	// Typed interactions are updated by Human actions, not arbitrary events.
	if !harnessWrite && meta["human"] != nil {
		return ErrConflict
	}
	output, _ := meta["output"].(map[string]any)
	if event.Seq <= m.Revision {
		if event.Seq == m.Revision && output["last_event"] == fingerprint {
			return nil
		}
		return ErrConflict
	}
	if event.Seq != m.Revision+1 {
		return ErrConflict
	}
	if (contract.Message{Status: contract.MessageStatus(m.Status)}).Ended() {
		return ErrConflict
	}
	if _, ref := event.Block["ref"]; ref {
		return fmt.Errorf("%w: complete block bodies are required", ErrInvalidContent)
	}
	if event.Block["type"] == storage.FrameType {
		return fmt.Errorf("%w: storage frames are not logical input blocks", ErrInvalidContent)
	}
	if event.Op == agentueui.OpSet || event.Op == agentueui.OpAppend {
		if id, ok := event.Block["id"].(string); ok {
			if err := c.materialize(id); err != nil {
				return err
			}
		}
	}
	next, err := agentueui.Apply(snapshot, event)
	if err != nil {
		return err
	}
	meta, _ = next["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		next["meta"] = meta
	}
	meta["output"] = map[string]any{"last_event": fingerprint}
	c.snapshot = next
	content, err := s.packContent(c)
	if err != nil {
		return err
	}
	if err := c.persist(s.contentMaxBytes); err != nil {
		return err
	}
	updates := map[string]any{"content": content, "revision": event.Seq}
	if event.Op == agentueui.OpEnd {
		updates["status"] = string(status)
	}
	if event.Op == agentueui.OpEnd && m.TargetKind != contract.ActorKindUser {
		updates["dispatch_pending"] = true
	}
	now := time.Now().UTC()
	at := now
	if event.Timestamp != nil {
		at = time.UnixMilli(*event.Timestamp).UTC()
	}
	if at.Before(m.CreatedAt) {
		updates["created_at"] = at
	}
	// TTL follows server acceptance, not a replayed provider timestamp.
	updates["updated_at"] = now
	return tx.Model(&m).UpdateColumns(updates).Error
}
