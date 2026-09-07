package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/compforge/loopd/server/internal/view"
)

func (s *Server) publishMessage(ctx context.Context, r *hertzapp.RequestContext) error {
	var input view.CreateMessageRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	var message contract.Message
	var err error
	if input.Actor.Kind == "" || input.Actor.Kind == contract.ActorKindUser {
		if input.Status != "" && input.Status != contract.MessageStatusCompleted {
			return service.ErrInvalid
		}
		userKey := input.Actor.Key
		if userKey == "" {
			userKey = input.UserKey
		}
		if s.Human != nil {
			userKey, err = s.identity(ctx, r)
			if err != nil {
				return err
			}
		}
		message, err = s.chat.Create(ctx, r.Param("conversation_id"), userKey, input.Target, input.Content)
	} else {
		message, err = s.messages.Publish(ctx, r.Param("conversation_id"), input.SpeakRequest)
	}
	if err != nil {
		return err
	}
	r.JSON(200, message.Info())
	return nil
}
func (s *Server) emitPublishedMessage(ctx context.Context, r *hertzapp.RequestContext) error {
	var input view.MessageEventRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	id, err := s.messages.EmitMessage(ctx, r.Param("message_id"), input.Event, input.Status)
	if err != nil {
		return err
	}
	r.JSON(202, view.MessageEventResponse{ID: id})
	return nil
}
