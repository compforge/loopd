package repo

import (
	"context"
	"github.com/compforge/loopd/server/internal/model"
)

// SaveOutput records stream content without turning persistence time into output activity time.
func (store *Store) SaveOutput(ctx context.Context, id string, content []byte, revision uint64) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return mapError(store.db.WithContext(ctx).Model(&model.Message{}).Where("id = ?", id).
		UpdateColumns(map[string]any{"content": content, "revision": revision}).Error)
}
