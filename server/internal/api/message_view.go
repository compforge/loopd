package api

import (
	"context"
	"encoding/json"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/service"
)

type humanResultView struct {
	loopd.HumanResult
	Message service.MessageView  `json:"message"`
	Reply   *service.MessageView `json:"reply,omitempty"`
}

func (s *Server) humanView(ctx context.Context, result loopd.HumanResult) (humanResultView, error) {
	messages := []loopd.Message{result.Message}
	if result.Reply != nil {
		messages = append(messages, *result.Reply)
	}
	views, err := s.messages.EnrichMessages(ctx, messages)
	if err != nil {
		return humanResultView{}, err
	}
	view := humanResultView{HumanResult: result, Message: views[0]}
	if len(views) > 1 {
		view.Reply = &views[1]
	}
	return view, nil
}

// Historical snapshots, live snapshots and acknowledgements use the same projection.
func (s *Server) messageEventData(ctx context.Context, id string, message *loopd.Message, event json.RawMessage) ([]byte, error) {
	var view *service.MessageView
	if message != nil {
		views, err := s.messages.EnrichMessages(ctx, []loopd.Message{*message})
		if err != nil {
			return nil, err
		}
		view = &views[0]
	}
	return json.Marshal(struct {
		MessageID string               `json:"message_id"`
		Message   *service.MessageView `json:"message,omitempty"`
		Event     json.RawMessage      `json:"event"`
	}{id, view, event})
}
