package api

import (
	"context"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	loopd "github.com/compforge/loopd"
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

func (server *Server) listConversations(ctx context.Context, request *hertzapp.RequestContext) error {
	if parentMessageID := request.Query("parent_message_id"); parentMessageID != "" {
		conversations, err := server.conversations.ListDetailConversations(ctx, parentMessageID)
		if err != nil {
			return err
		}
		request.JSON(consts.StatusOK, page[loopd.Conversation]{Data: conversations})
		return nil
	}
	limit, err := queryLimit(request)
	if err != nil {
		return err
	}
	conversations, err := server.conversations.ListConversations(ctx, string(request.Query("before")), limit)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Conversation]{Data: conversations})
	return nil
}
