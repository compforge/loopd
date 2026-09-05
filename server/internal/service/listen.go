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
	Signal(context.Context, string, string, loopd.ActorRef) error
	Listen(context.Context, string, loopd.ActorRef, conversationclient.ReadMessages) (loopd.ListenResult, error)
}

type InboxRepository interface {
	GetConversation(context.Context, string) (model.Conversation, error)
	ListInbox(context.Context, string, string, string, string, int) ([]model.Message, error)
	PendingDispatches(context.Context, int) ([]model.Message, error)
	AcknowledgeDispatch(context.Context, string) error
}

// ListenService coordinates message notifications and Kubernetes receipt cursors.
type ListenService struct {
	repo          InboxRepository
	conversations ConversationCoordinator
	logger        *slog.Logger
}

func NewListenService(repository InboxRepository, conversations ConversationCoordinator, logger *slog.Logger) *ListenService {
	return &ListenService{repo: repository, conversations: conversations, logger: loggerOrDefault(logger)}
}

func (s *ListenService) Listen(ctx context.Context, conversationID string, request loopd.ListenRequest) (loopd.ListenResult, error) {
	if !request.Actor.ValidTarget() {
		return loopd.ListenResult{}, ErrInvalid
	}
	if _, err := s.repo.GetConversation(ctx, conversationID); err != nil {
		return loopd.ListenResult{}, err
	}
	result, err := s.conversations.Listen(ctx, conversationID, request.Actor, func(ctx context.Context, after string) ([]loopd.Message, error) {
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
			"message_count", len(result.Messages), "cursor", result.LastMessageID)
	}
	return result, err
}

// Notify runs only after SQL commit. Failure leaves the Message pending so any
// server replica can retry without creating a second user message.
func (s *ListenService) Notify(ctx context.Context, message model.Message) error {
	if err := s.conversations.Signal(ctx, message.ConversationID, message.ID,
		loopd.ActorRef{Kind: loopd.Role(message.TargetKind), Key: message.TargetKey}); err != nil {
		return err
	}
	if err := s.repo.AcknowledgeDispatch(ctx, message.ID); err != nil {
		return err
	}
	s.logger.DebugContext(ctx, "conversation participant notified", "conversation_id", message.ConversationID,
		"message_id", message.ID, "target_kind", message.TargetKind, "target_key", message.TargetKey)
	return nil
}

func (s *ListenService) Maintain(ctx context.Context) error {
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

func (s *ListenService) Run(ctx context.Context) {
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
