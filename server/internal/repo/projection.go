package repo

import (
	"context"
	"crypto/sha256"
	"fmt"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// ProjectOutput maintains the visible snapshot for conversation-scoped output
// before attempting page delivery. SQL serializes revisions and remembers the
// last event fingerprint so an ambiguous response can be retried without appending twice.
func (s *Store) ProjectOutput(ctx context.Context, id string, event agentueui.Event) error {
	data, err := event.Marshal()
	if err != nil {
		return err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "id = ?", id).Error; err != nil {
			return err
		}
		c, err := openMessageContent(tx, m)
		if err != nil {
			return err
		}
		snapshot := c.snapshot
		meta, _ := snapshot["meta"].(map[string]any)
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
		if (loopd.Message{Content: m.Content}).Ended() {
			return ErrConflict
		}
		if _, ref := event.Block["ref"]; ref {
			return fmt.Errorf("%w: complete block bodies are required", ErrInvalidContent)
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
		meta["output"] = map[string]any{"ended": event.Op == agentueui.OpEnd, "last_event": fingerprint}
		c.snapshot = next
		content, err := s.packContent(c)
		if err != nil {
			return err
		}
		if err := c.persist(); err != nil {
			return err
		}
		updates := map[string]any{"content": content, "revision": event.Seq}
		if event.Op == agentueui.OpEnd && m.TargetKind != "user" {
			updates["dispatch_pending"] = true
		}
		at := time.Now().UTC()
		if event.Timestamp != nil {
			at = time.UnixMilli(*event.Timestamp).UTC()
		}
		if at.Before(m.CreatedAt) {
			updates["created_at"] = at
		}
		if at.After(m.UpdatedAt) {
			updates["updated_at"] = at
		}
		return tx.Model(&m).UpdateColumns(updates).Error
	})
}
