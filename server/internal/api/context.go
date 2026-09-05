package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func (server *Server) getMessageContext(ctx context.Context, request *hertzapp.RequestContext) error {
	value, err := server.context.GetMessageContext(ctx, request.Param("conversation_id"), request.Param("message_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, value)
	return nil
}
