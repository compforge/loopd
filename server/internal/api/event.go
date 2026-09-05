package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/compforge/loopd/server/internal/delivery"
)

func (server *Server) completeDelivery(ctx context.Context, request *hertzapp.RequestContext) error {
	var input completeDeliveryRequest
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
