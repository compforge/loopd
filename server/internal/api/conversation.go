package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func (server *Server) createConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createConversationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	conversation, err := server.conversations.CreateConversation(ctx, input.Name, input.ParentMessageID)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusCreated, conversation)
	return nil
}

func (server *Server) getConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	conversation, err := server.conversations.GetConversation(ctx, request.Param("conversation_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, conversation)
	return nil
}
