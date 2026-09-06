package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/view"
)

func (s *Server) publishMessage(ctx context.Context, r *hertzapp.RequestContext) error {
	var input contract.SpeakRequest
	if err := decodeBody(r, &input); err != nil {
		return err
	}
	message, err := s.messages.Speak(ctx, r.Param("conversation_id"), input)
	if err != nil {
		return err
	}
	views, err := s.messages.EnrichMessages(ctx, []contract.Message{message})
	if err != nil {
		return err
	}
	r.JSON(200, views[0])
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
