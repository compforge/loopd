package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/delivery"
)

func (server *Server) appendTaskEvent(ctx context.Context, request *hertzapp.RequestContext) error {
	var input appendTaskEventRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	eventID, err := server.chat.Emit(ctx, request.Param("task_id"), input.Event)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusAccepted, appendTaskEventResponse{ID: eventID})
	return nil
}

func (server *Server) completeTask(ctx context.Context, request *hertzapp.RequestContext) error {
	var input completeTaskRequest
	if len(request.Request.Body()) != 0 {
		if err := decodeBody(request, &input); err != nil {
			return err
		}
	}
	var failure *delivery.Failure
	if input.Error != nil {
		failure = &delivery.Failure{Code: input.Error.Code, Message: input.Error.Message}
	}
	if err := server.chat.Complete(ctx, request.Param("task_id"), failure); err != nil {
		return err
	}
	request.SetStatusCode(consts.StatusNoContent)
	return nil
}

func (server *Server) createOutput(ctx context.Context, request *hertzapp.RequestContext) error {
	var input loopd.OutputRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	message, err := server.chat.Output(ctx, request.Param("task_id"), input)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, message)
	return nil
}
func (server *Server) appendMessageEvent(ctx context.Context, request *hertzapp.RequestContext) error {
	var input appendTaskEventRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	id, err := server.chat.EmitMessage(ctx, request.Param("task_id"), request.Param("message_id"), input.Event)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusAccepted, appendTaskEventResponse{ID: id})
	return nil
}
