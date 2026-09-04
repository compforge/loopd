package api

import (
	"context"
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
			return writer.WriteEvent(event.Cursor, "", event.Data)
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
