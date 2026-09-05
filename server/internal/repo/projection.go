package repo

import (
	"context"
	"encoding/json"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectOutput maintains the visible snapshot for conversation-scoped output
// without depending on a UI stream's lifetime. Redis owns delivery ordering;
// retries after a SQL failure reuse the accepted event and advance this snapshot.
func (s *Store) ProjectOutput(ctx context.Context, id string, event agentueui.Event) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, "id = ?", id).Error; err != nil {
			return err
		}
		if event.Seq <= m.Revision {
			return nil
		}
		if event.Seq != m.Revision+1 {
			return ErrConflict
		}
		var snapshot map[string]any
		if err := json.Unmarshal(m.Content, &snapshot); err != nil {
			return err
		}
		next, err := agentueui.Apply(snapshot, event)
		if err != nil {
			return err
		}
		content, err := agentueui.MarshalSnapshot(next)
		if err != nil {
			return err
		}
		return tx.Model(&m).UpdateColumns(map[string]any{"content": content, "revision": event.Seq}).Error
	})
}
