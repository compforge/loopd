// Package api adapts loop-server use cases to Hertz HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

const defaultPageSize = 100

type Server struct {
	service *service.Service
	logger  *slog.Logger
}

func New(loopService *service.Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{service: loopService, logger: logger}
}

func (server *Server) Register(engine *route.Engine) {
	engine.GET("/healthz", func(_ context.Context, request *hertzapp.RequestContext) {
		request.JSON(consts.StatusOK, map[string]bool{"ok": true})
	})
	engine.GET("/v1/responders", server.adapt(server.listResponders))
	engine.POST("/v1/conversations", server.adapt(server.createConversation))
	engine.GET("/v1/conversations/:conversation_id", server.adapt(server.getConversation))
	engine.GET("/v1/conversations/:conversation_id/messages", server.adapt(server.listMessages))
	engine.POST("/v1/conversations/:conversation_id/messages", server.adapt(server.createMessage))
}

type handler func(context.Context, *hertzapp.RequestContext) error

func (server *Server) adapt(next handler) hertzapp.HandlerFunc {
	return func(ctx context.Context, request *hertzapp.RequestContext) {
		if err := next(ctx, request); err != nil {
			server.writeError(request, err)
		}
	}
}

func (server *Server) writeError(request *hertzapp.RequestContext, err error) {
	status := consts.StatusInternalServerError
	typeName := "internal_error"
	switch {
	case errors.Is(err, service.ErrInvalid):
		status, typeName = consts.StatusBadRequest, "invalid_request"
	case errors.Is(err, service.ErrConflict), errors.Is(err, repo.ErrConflict):
		status, typeName = consts.StatusConflict, "conflict"
	case errors.Is(err, repo.ErrNotFound):
		status, typeName = consts.StatusNotFound, "not_found"
	default:
		server.logger.Error("loop-server request failed", "error", err)
	}
	request.JSON(status, errorResponse{Error: apiError{Type: typeName, Message: err.Error()}})
}

func (server *Server) listResponders(_ context.Context, request *hertzapp.RequestContext) error {
	request.JSON(consts.StatusOK, page[loopd.Responder]{Data: server.service.Responders()})
	return nil
}

func (server *Server) createConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createConversationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	conversation, err := server.service.CreateConversation(ctx, input.Name, input.ParentMessageID)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusCreated, conversation)
	return nil
}

func (server *Server) getConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	conversation, err := server.service.GetConversation(ctx, request.Param("conversation_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, conversation)
	return nil
}

func (server *Server) listMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	limit, err := queryLimit(request)
	if err != nil {
		return err
	}
	messages, err := server.service.ListMessages(
		ctx, request.Param("conversation_id"), string(request.Query("after")), limit,
	)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Message]{Data: messages})
	return nil
}

func (server *Server) createMessage(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createMessageRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	message, err := server.service.CreateMessage(
		ctx, request.Param("conversation_id"), input.Kind, input.Key, input.Content,
	)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusCreated, message)
	return nil
}

func decodeBody(request *hertzapp.RequestContext, target any) error {
	if len(request.Request.Body()) == 0 {
		return fmt.Errorf("%w: request body is required", service.ErrInvalid)
	}
	if err := json.Unmarshal(request.Request.Body(), target); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", service.ErrInvalid, err)
	}
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
