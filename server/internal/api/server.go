// Package api adapts loop-server use cases to Hertz HTTP endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/compforge/loopd/server/internal/repo"
	"github.com/compforge/loopd/server/internal/service"
)

type Server struct {
	Human         *service.HumanService
	HumanIdentity HumanIdentity
	actors        *service.ActorService
	conversations *service.ConversationService
	messages      *service.MessageService
	chat          *service.ChatService
	tasks         *service.TaskService
	logger        *slog.Logger
}

func New(
	actors *service.ActorService,
	conversations *service.ConversationService,
	messages *service.MessageService,
	chat *service.ChatService,
	tasks *service.TaskService,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{actors: actors, conversations: conversations, messages: messages, chat: chat, tasks: tasks, logger: logger}
}

func (server *Server) Register(engine *route.Engine) {
	engine.GET("/healthz", func(_ context.Context, request *hertzapp.RequestContext) {
		request.JSON(consts.StatusOK, map[string]bool{"ok": true})
	})
	engine.GET("/v1/actors", server.adapt(server.listActors))
	engine.PUT("/v1/operators/:key", server.adapt(server.registerOperator))
	engine.PUT("/v1/harnesses/:key", server.adapt(server.registerHarness))
	engine.POST("/v1/conversations", server.adapt(server.createConversation))
	engine.GET("/v1/conversations", server.adapt(server.listConversations))
	engine.GET("/v1/conversations/:conversation_id", server.adapt(server.getConversation))
	engine.GET("/v1/conversations/:conversation_id/messages", server.adapt(server.listMessages))
	engine.POST("/v1/conversations/:conversation_id/messages", server.adapt(server.createChatMessages))
	engine.POST("/v1/tasks/:task_id/human", server.adapt(server.createHuman))
	engine.GET("/v1/human/:message_id", server.adapt(server.getHuman))
	engine.POST("/v1/conversations/:conversation_id/tasks/:task_id/replies", server.adapt(server.replyHuman))
	engine.GET("/v1/tasks/:task_id", server.adapt(server.getTask))
	engine.POST("/v1/tasks/:task_id/events", server.adapt(server.appendTaskEvent))
	engine.POST("/v1/tasks/:task_id/complete", server.adapt(server.completeTask))
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
	message := err.Error()
	switch {
	case errors.Is(err, repo.ErrForbidden):
		status, typeName = consts.StatusForbidden, "forbidden"
	case errors.Is(err, service.ErrInvalid):
		status, typeName = consts.StatusBadRequest, "invalid_request"
	case errors.Is(err, service.ErrConflict), errors.Is(err, repo.ErrConflict):
		status, typeName = consts.StatusConflict, "conflict"
	case errors.Is(err, service.ErrUnavailable):
		status, typeName = consts.StatusServiceUnavailable, "service_unavailable"
		message = service.ErrUnavailable.Error()
	case errors.Is(err, repo.ErrNotFound):
		status, typeName = consts.StatusNotFound, "not_found"
	default:
		server.logger.Error("loop-server request failed", "error", err)
	}
	request.JSON(status, errorResponse{Error: apiError{Type: typeName, Message: message}})
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
