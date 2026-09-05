package service

import (
	"context"
	"errors"
	"time"
)

// Run reconciles persisted completion intent even for tasks that never ask a Human.
func (service *ChatService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := service.Maintain(ctx); err != nil && ctx.Err() == nil {
			service.logger.ErrorContext(ctx, "resume task completion", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (service *ChatService) Maintain(ctx context.Context) error {
	rows, err := service.repo.PendingCompletions(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := service.resumeCompletion(ctx, row.TaskID, row.Completion); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
