package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
)

type HumanService struct {
	store *repo.Store
	tasks interface {
		Wake(context.Context, string) error
		Exists(context.Context, string) (bool, error)
	}
	logger *slog.Logger
}

func NewHumanService(store *repo.Store, tasks interface {
	Wake(context.Context, string) error
	Exists(context.Context, string) (bool, error)
}, logger *slog.Logger) *HumanService {
	return &HumanService{store: store, tasks: tasks, logger: loggerOrDefault(logger)}
}
func (s *HumanService) Create(ctx context.Context, r loopd.HumanRequest) (loopd.HumanResult, error) {
	if err := r.Validate(); err != nil {
		return loopd.HumanResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	active, err := s.tasks.Exists(ctx, r.TaskID)
	if err != nil {
		return loopd.HumanResult{}, err
	}
	result, err := s.store.CreateHuman(ctx, r, active)
	return result, humanError(err)
}
func (s *HumanService) Get(ctx context.Context, id string) (loopd.HumanResult, error) {
	return s.store.GetHuman(ctx, id)
}
func (s *HumanService) Reply(ctx context.Context, conversationID, taskID, actor string, r loopd.HumanReply) (loopd.HumanResult, error) {
	if r.ReplyToMessageID == "" {
		return loopd.HumanResult{}, ErrInvalid
	}
	result, err := s.store.ReplyHuman(ctx, conversationID, taskID, actor, r)
	return result, humanError(err)
}
func humanError(err error) error {
	if errors.Is(err, repo.ErrInvalidHuman) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}

// Run persists deadlines without a browser or Operator, retries wake delivery,
// and finishes interrupted Task completion from the original persisted intent.
func (s *HumanService) Run(ctx context.Context, chat *ChatService) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.Maintain(ctx, chat); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "maintain Human messages", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *HumanService) Maintain(ctx context.Context, chat *ChatService) error {
	rows, err := s.store.HumanMaintenance(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if row.DeliveryState == "closing" {
			if err := chat.resumeCompletion(ctx, row.TaskID, row.Completion); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if row.HumanDueAt != nil {
			if _, err := s.Get(ctx, row.ID); err != nil {
				failures = append(failures, err)
				continue
			}
			row, err = s.store.GetMessage(ctx, row.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if row.WakePending {
			if err := s.tasks.Wake(ctx, row.TaskID); err != nil {
				failures = append(failures, fmt.Errorf("wake task %s: %w", row.TaskID, err))
				continue
			}
			if err := s.store.AcknowledgeHumanWake(ctx, row.ID, row.Revision); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
