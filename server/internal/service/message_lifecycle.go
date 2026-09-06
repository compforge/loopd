package service

import (
	"context"
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

// Run owns output expiry independently of page connections and Human deadlines.
func (service *MessageService) Run(ctx context.Context, ttl time.Duration) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := service.Expire(ctx, time.Now().UTC().Add(-ttl)); err != nil && ctx.Err() == nil {
			service.logger.ErrorContext(ctx, "expire message outputs", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (service *MessageService) Expire(ctx context.Context, cutoff time.Time) error {
	ids, err := service.repo.ExpireMessages(ctx, cutoff, 100)
	if len(ids) > 0 {
		service.logger.InfoContext(ctx, "message outputs expired", "count", len(ids), "message_ids", ids)
	}
	return err
}

// MessageChanges checks small metadata first; unchanged bodies and Parts stay in DB.
func (service *MessageService) MessageChanges(ctx context.Context, convID string, revisions map[string]uint64) ([]contract.Message, error) {
	if len(revisions) > maxPageSize {
		return nil, ErrInvalid
	}
	ids := make([]string, 0, len(revisions))
	for id := range revisions {
		ids = append(ids, id)
	}
	states, err := service.repo.GetMessageStates(ctx, convID, ids)
	if err != nil {
		return nil, err
	}
	ids = ids[:0]
	for _, state := range states {
		if state.Revision > revisions[state.ID] {
			ids = append(ids, state.ID)
		}
	}
	rows, err := service.repo.GetMessages(ctx, convID, ids)
	if err != nil {
		return nil, err
	}
	result := make([]contract.Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, messageFromModel(row))
	}
	return result, nil
}
