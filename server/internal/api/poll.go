package api

import (
	"context"
	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/service"
)

func (server *Server) pollConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	if server.Poll == nil {
		return service.ErrUnavailable
	}
	var input contract.PollRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	result, err := server.Poll.Poll(ctx, request.Param("conversation_id"), input)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, result)
	return nil
}

func (server *Server) commitConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	if server.Poll == nil {
		return service.ErrUnavailable
	}
	var input contract.CommitRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	if err := server.Poll.Commit(ctx, request.Param("conversation_id"), input); err != nil {
		return err
	}
	request.Status(consts.StatusNoContent)
	return nil
}
