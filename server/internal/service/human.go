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
	store  *repo.Store
	logger *slog.Logger
}

func NewHumanService(store *repo.Store, logger *slog.Logger) *HumanService {
	return &HumanService{store: store, logger: loggerOrDefault(logger)}
}
func (s *HumanService) Create(ctx context.Context, r loopd.HumanRequest) (loopd.HumanResult, error) {
	if err := r.Validate(); err != nil {
		return loopd.HumanResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	result, err := s.store.CreateHuman(ctx, r)
	return result, humanError(err)
}
func (s *HumanService) Get(ctx context.Context, id string) (loopd.HumanResult, error) {
	return s.store.GetHuman(ctx, id)
}
func (s *HumanService) Reply(ctx context.Context, conversationID, actor string, r loopd.HumanReply) (loopd.HumanResult, error) {
	if r.ReplyToID == "" {
		return loopd.HumanResult{}, ErrInvalid
	}
	result, err := s.store.ReplyHuman(ctx, conversationID, actor, r)
	return result, humanError(err)
}
func humanError(err error) error {
	if errors.Is(err, repo.ErrInvalidHuman) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}

// Run advances Human deadlines independently of connected clients.
func (s *HumanService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.Maintain(ctx); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "maintain Human messages", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *HumanService) Maintain(ctx context.Context) error {
	rows, err := s.store.HumanMaintenance(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
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
			// Replies are delivered through Conv notifications; handle polling observes deadlines.
			if err := s.store.AcknowledgeHumanWake(ctx, row.ID, row.Revision); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
