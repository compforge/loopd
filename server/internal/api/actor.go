package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/view"
)

func (server *Server) listActors(ctx context.Context, request *hertzapp.RequestContext) error {
	actors, err := server.actors.List(ctx)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, view.Page[loopd.Actor]{Data: actors})
	return nil
}
