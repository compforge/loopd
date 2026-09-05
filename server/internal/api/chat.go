package api

import (
	"context"
	"encoding/json"
	"errors"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	hertzsse "github.com/cloudwego/hertz/pkg/protocol/sse"
	"github.com/compforge/loopd/server/internal/delivery"
)

const taskIDHeader = "X-Loopd-Task-ID"

func (server *Server) createChatMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createChatMessagesRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	conversationID := request.Param("conversation_id")
	taskID := input.TaskID
	if taskID == "" {
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
		taskID = message.TaskID
	}
	request.Response.Header.Set(taskIDHeader, taskID)
	var writer *hertzsse.Writer
	streamErr := server.chat.Stream(
		ctx,
		conversationID,
		taskID,
		hertzsse.GetLastEventID(&request.Request),
		func(event delivery.Event) error {
			if writer == nil {
				writer = hertzsse.NewWriter(request)
			}
			data := event.Data
			if event.MessageID != "" {
				var err error
				data, err = json.Marshal(struct {
					MessageID string          `json:"message_id"`
					Message   any             `json:"message,omitempty"`
					Event     json.RawMessage `json:"event"`
				}{event.MessageID, event.Message, event.Data})
				if err != nil {
					return err
				}
			}
			return writer.WriteEvent(event.ID, "", data)
		},
	)
	if errors.Is(streamErr, context.Canceled) {
		streamErr = nil
	}
	if writer == nil {
		return streamErr
	}
	closeErr := writer.Close()
	if err := errors.Join(streamErr, closeErr); err != nil {
		// Headers may already be on the wire. Logging is safe; returning the
		// error to the generic adapter would append a JSON error to the SSE body.
		server.logger.ErrorContext(ctx, "chat stream stopped", "task_id", taskID, "error", err)
	}
	return nil
}
