package component

import (
	"context"
	"log/slog"
	"time"
)

type MessageRepository interface {
	ExpireMessages(context.Context, time.Time, int) ([]string, error)
}

// MessageGC runs independently of page listeners. It closes inactive output,
// retaining conversation history; it never owns Harness execution.
type MessageGC struct {
	repo          MessageRepository
	ttl, interval time.Duration
	batchSize     int
	logger        *slog.Logger
}

func NewMessageGC(repo MessageRepository, ttl, interval time.Duration, batchSize int, logger *slog.Logger) *MessageGC {
	if logger == nil {
		logger = slog.Default()
	}
	return &MessageGC{repo: repo, ttl: ttl, interval: interval, batchSize: batchSize, logger: logger}
}

func (g *MessageGC) Run(ctx context.Context) {
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := g.Sweep(ctx); err != nil && ctx.Err() == nil {
			g.logger.ErrorContext(ctx, "message gc failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep performs one bounded pass. Conditional updates make concurrent instances
// safe without a global lease or a second lifecycle table.
func (g *MessageGC) Sweep(ctx context.Context) error {
	ids, err := g.repo.ExpireMessages(ctx, time.Now().UTC().Add(-g.ttl), g.batchSize)
	if len(ids) > 0 {
		g.logger.InfoContext(ctx, "message outputs expired", "count", len(ids), "message_ids", ids)
	}
	return err
}
