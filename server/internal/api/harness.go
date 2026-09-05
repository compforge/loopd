package api

import (
	"context"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func (server *Server) registerHarness(ctx context.Context, request *hertzapp.RequestContext) error {
	var input registrationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	harness, err := server.actors.RegisterHarness(
		ctx,
		request.Param("key"),
		input.DisplayName,
		input.Description,
		time.Duration(input.LeaseSeconds)*time.Second,
	)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, harness)
	return nil
}
