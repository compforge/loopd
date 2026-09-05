package api

import (
	"context"
	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/service"
)

func (server *Server) listenConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	if server.Listen == nil {
		return service.ErrUnavailable
	}
	var input loopd.ListenRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	result, err := server.Listen.Listen(ctx, request.Param("conversation_id"), input)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, result)
	return nil
}
