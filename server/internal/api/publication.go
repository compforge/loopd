package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/compforge/loopd/server/internal/view"
)

func (s *Server) publishMessage(ctx context.Context, r *hertzapp.RequestContext) error {
	var input loopd.SpeakRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	message, err := s.messages.Speak(ctx, r.Param("conversation_id"), input)
	if err != nil {
		return err
	}
	views, err := s.messages.EnrichMessages(ctx, []loopd.Message{message})
	if err != nil {
		return err
	}
	r.JSON(200, views[0])
	return nil
}
func (s *Server) actorConversation(ctx context.Context, r *hertzapp.RequestContext) error {
	var actor loopd.ActorRef
	if err := decodeBody(r, &actor); err != nil {
		return err
	}
	if !actor.ValidTarget() {
		return service.ErrInvalid
	}
	conv, err := s.conversations.ActorConversation(ctx, r.Param("conversation_id"), actor)
	if err != nil {
		return err
	}
	r.JSON(200, conv)
	return nil
}
func (s *Server) emitPublishedMessage(ctx context.Context, r *hertzapp.RequestContext) error {
	var input view.MessageEventRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	id, err := s.chat.EmitMessage(ctx, r.Param("message_id"), input.Event, input.Status)
	if err != nil {
		return err
	}
	r.JSON(202, view.MessageEventResponse{ID: id})
	return nil
}
