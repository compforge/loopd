package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func (server *Server) getTask(ctx context.Context, request *hertzapp.RequestContext) error {
	task, err := server.tasks.GetContext(ctx, request.Param("task_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, task)
	return nil
}
