package api

import (
	"context"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
)

func (server *Server) registerActor(ctx context.Context, request *hertzapp.RequestContext) error {
	var input registerActorRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	actor, err := server.actors.Register(ctx, loopd.Actor{
		ActorRef: loopd.ActorRef{
			Kind: loopd.Role(request.Param("kind")),
			Key:  request.Param("key"),
		},
		DisplayName: input.DisplayName,
		Description: input.Description,
	}, time.Duration(input.LeaseSeconds)*time.Second)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, actor)
	return nil
}

func (server *Server) listActors(ctx context.Context, request *hertzapp.RequestContext) error {
	actors, err := server.actors.List(ctx)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Actor]{Data: actors})
	return nil
}
