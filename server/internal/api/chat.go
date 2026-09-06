package api

import (
	"context"
	"encoding/json"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	hertzsse "github.com/cloudwego/hertz/pkg/protocol/sse"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/view"
)

const taskIDHeader = "X-Loopd-Task-ID"

func (server *Server) createChatMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	var input view.CreateChatMessagesRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	conversationID := request.Param("conversation_id")
	if server.Human != nil {
		identity, err := server.identity(ctx, request)
		if err != nil {
			return err
		}
		input.UserKey = identity
	}
	message, err := server.chat.Create(ctx, conversationID, input.UserKey, input.Target, input.Content)
	if err != nil {
		return err
	}
	accepted := &message
	taskID := message.TaskID
	request.Response.Header.Set(taskIDHeader, taskID)
	// Input acknowledgement is independent of the Conv listener and Redis.
	start, err := ui.Start(accepted.Content, accepted.Revision)
	if err != nil {
		return err
	}
	raw, err := start.Marshal()
	if err != nil {
		return err
	}
	data, err := server.messageEventData(ctx, accepted.ID, accepted, raw)
	if err != nil {
		return err
	}
	writer := hertzsse.NewWriter(request)
	if err := writer.WriteEvent("", "", data); err != nil {
		server.logger.WarnContext(ctx, "input accepted but page disconnected", "task_id", taskID, "error", err)
		_ = writer.Close()
		return nil
	}
	// Input submission is acknowledged once. The page independently subscribes
	// to its Conv; it does not need an input-owned connection to observe actors.
	end, _ := ui.End(accepted.Revision).Marshal()
	endData, err := server.messageEventData(ctx, accepted.ID, accepted, end)
	if err == nil {
		err = writer.WriteEvent("", "", endData)
	}
	if err != nil {
		server.logger.WarnContext(ctx, "input acknowledgement ended early", "message_id", accepted.ID, "error", err)
	}
	_ = writer.Close()
	return nil
}

// Historical snapshots, live snapshots and acknowledgements use the same projection.
func (s *Server) messageEventData(ctx context.Context, id string, message *contract.Message, event json.RawMessage) ([]byte, error) {
	var projected *view.Message
	if message != nil {
		values, err := s.messages.EnrichMessages(ctx, []contract.Message{*message})
		if err != nil {
			return nil, err
		}
		projected = &values[0]
	}
	return json.Marshal(view.MessageEvent{MessageID: id, Message: projected, Event: event})
}
