package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func (server *Server) createChatMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createChatMessagesRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	message, err := server.chat.Create(
		ctx, request.Param("conversation_id"), input.UserKey, input.Responder, input.Content,
	)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusCreated, message)
	return nil
}
