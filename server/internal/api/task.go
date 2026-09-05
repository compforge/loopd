package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
)

func (server *Server) getTask(ctx context.Context, request *hertzapp.RequestContext) error {
	task, err := server.tasks.GetContext(ctx, request.Param("task_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, task)
	return nil
}

func (server *Server) listTaskMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	limit, err := queryLimit(request)
	if err != nil {
		return err
	}
	messages, err := server.tasks.ListMessages(ctx, request.Param("task_id"), request.Query("after"), limit)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Message]{Data: messages})
	return nil
}
