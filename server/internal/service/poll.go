package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	loopd "github.com/compforge/loopd"
	conversationclient "github.com/compforge/loopd/server/internal/conversation"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type ConversationCoordinator interface {
	Signal(context.Context, string, string, loopd.ActorRef, uint64) error
	Poll(context.Context, string, loopd.ActorRef, string, conversationclient.ReadMessages) (loopd.PollResult, error)
	Commit(context.Context, string, loopd.CommitRequest) error
}

type InboxRepository interface {
	GetConversation(context.Context, string) (model.Conversation, error)
	ListInbox(context.Context, string, string, string, string, int) ([]model.Message, error)
	PendingDispatches(context.Context, int) ([]model.Message, error)
	AcknowledgeDispatch(context.Context, string) error
}

// PollService coordinates message notifications and Kubernetes receipt cursors.
type PollService struct {
	repo          InboxRepository
	conversations ConversationCoordinator
	logger        *slog.Logger
}

func NewPollService(repository InboxRepository, conversations ConversationCoordinator, logger *slog.Logger) *PollService {
	return &PollService{repo: repository, conversations: conversations, logger: loggerOrDefault(logger)}
}

func (s *PollService) Poll(ctx context.Context, conversationID string, request loopd.PollRequest) (loopd.PollResult, error) {
	if !request.Actor.ValidTarget() {
		return loopd.PollResult{}, ErrInvalid
	}
	if _, err := s.repo.GetConversation(ctx, conversationID); err != nil {
		return loopd.PollResult{}, err
	}
	result, err := s.conversations.Poll(ctx, conversationID, request.Actor, request.After, func(ctx context.Context, after string) ([]loopd.Message, error) {
		rows, err := s.repo.ListInbox(ctx, conversationID, string(request.Actor.Kind), request.Actor.Key, after, pageSize(request.Limit))
		if err != nil {
			return nil, err
		}
		messages := make([]loopd.Message, len(rows))
		for i, row := range rows {
			messages[i] = messageFromModel(row)
		}
		return messages, nil
	})
	if apierrors.IsNotFound(err) {
		return result, repo.ErrNotFound
	}
	if errors.Is(err, conversationclient.ErrNotParticipant) {
		return result, repo.ErrForbidden
	}
	if apierrors.IsConflict(err) {
		return result, ErrConflict
	}
	if err == nil && len(result.Messages) > 0 {
		s.logger.InfoContext(ctx, "conversation messages received", "conversation_id", conversationID,
			"actor_kind", request.Actor.Kind, "actor_key", request.Actor.Key,
			"message_count", len(result.Messages), "cursor", result.Position)
	}
	return result, err
}

func (s *PollService) Commit(ctx context.Context, conversationID string, request loopd.CommitRequest) error {
	err := s.conversations.Commit(ctx, conversationID, request)
	switch {
	case apierrors.IsNotFound(err):
		return repo.ErrNotFound
	case errors.Is(err, conversationclient.ErrNotParticipant):
		return repo.ErrForbidden
	case errors.Is(err, conversationclient.ErrInvalidCommit):
		return ErrInvalid
	case apierrors.IsConflict(err):
		return ErrConflict
	}
	if err == nil {
		s.logger.InfoContext(ctx, "conversation consumption committed", "conversation_id", conversationID,
			"actor_kind", request.Actor.Kind, "actor_key", request.Actor.Key, "through", request.Through)
	}
	return err
}

// Notify runs only after SQL commit. Failure leaves the Message pending so any
// server replica can retry without creating a second user message.
func (s *PollService) Notify(ctx context.Context, message model.Message) error {
	if err := s.conversations.Signal(ctx, message.ConversationID, message.ID,
		loopd.ActorRef{Kind: loopd.Role(message.TargetKind), Key: message.TargetKey}, message.Revision); err != nil {
		return err
	}
	if err := s.repo.AcknowledgeDispatch(ctx, message.ID); err != nil {
		return err
	}
	s.logger.DebugContext(ctx, "conversation participant notified", "conversation_id", message.ConversationID,
		"message_id", message.ID, "target_kind", message.TargetKind, "target_key", message.TargetKey)
	return nil
}

func (s *PollService) Maintain(ctx context.Context) error {
	rows, err := s.repo.PendingDispatches(ctx, maxPageSize)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.Notify(ctx, row); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *PollService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.Maintain(ctx); err != nil && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "notify conversation participants", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
