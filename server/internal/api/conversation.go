package api

import (
	"context"
	"errors"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	hertzsse "github.com/cloudwego/hertz/pkg/protocol/sse"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/component"
	"github.com/compforge/loopd/server/internal/view"
)

func (server *Server) createConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input view.CreateConversationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	userKey, err := server.identity(ctx, request)
	if err != nil {
		return err
	}
	conversation, err := server.conversations.CreateConversation(ctx, input.Name, userKey)
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
	if parentID := request.Query("parent_id"); parentID != "" {
		values, err := server.conversations.FindActorConversation(ctx, parentID, contract.ActorKind(request.Query("actor_kind")), request.Query("actor_key"))
		if err != nil {
			return err
		}
		request.JSON(consts.StatusOK, view.Page[contract.Conversation]{Data: values})
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
	request.JSON(consts.StatusOK, view.Page[contract.Conversation]{Data: conversations})
	return nil
}

// streamConversation multiplexes message-addressed events for exactly one Conv.
func (server *Server) streamConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	convID := request.Param("conversation_id")
	if _, err := server.conversations.GetConversation(ctx, convID); err != nil {
		return err
	}
	var writer *hertzsse.Writer
	err := server.Listen(ctx, convID, func(event component.Event) error {
		data := event.Data
		if event.MessageID != "" {
			var err error
			data, err = server.messageEventData(ctx, event.MessageID, event.Message, data)
			if err != nil {
				return err
			}
		}
		if writer == nil {
			writer = hertzsse.NewWriter(request)
		}
		// Redis cursors are message-local; there is no shared Conv event cursor.
		return writer.WriteEvent("", "", data)
	})
	if writer == nil {
		return err
	}
	closeErr := writer.Close()
	if !errors.Is(err, context.Canceled) && errors.Join(err, closeErr) != nil {
		server.logger.WarnContext(ctx, "conversation stream stopped", "conversation_id", convID, "error", errors.Join(err, closeErr))
	}
	return nil
}
