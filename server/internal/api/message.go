package api

import (
	"context"
	"fmt"
	"strconv"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/service"
)

const defaultPageSize = 100

func (server *Server) listMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	limit, err := queryLimit(request)
	if err != nil {
		return err
	}
	messages, err := server.messages.ListMessages(
		ctx, request.Param("conversation_id"), string(request.Query("after")), limit,
	)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Message]{Data: messages})
	return nil
}

func queryLimit(request *hertzapp.RequestContext) (int, error) {
	value := request.Query("limit")
	if value == "" {
		return defaultPageSize, nil
	}
	limit, err := strconv.Atoi(string(value))
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("%w: limit must be a positive integer", service.ErrInvalid)
	}
	return limit, nil
}
