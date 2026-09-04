// Package server implements loop-server's durable collaboration API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/harness"
)

type Server struct {
	store      *Store
	responders []loopd.Responder
	known      map[loopd.ResponderRef]struct{}
	runner     *Runner
	logger     *slog.Logger
	streamPoll time.Duration
	streamPing time.Duration
}

type Config struct {
	Responders []loopd.Responder
	Adapters   map[string]harness.Adapter
	Runner     RunnerConfig
	StreamPoll time.Duration
	StreamPing time.Duration
	Logger     *slog.Logger
}

func New(store *Store, config Config) *Server {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.StreamPoll <= 0 {
		config.StreamPoll = 500 * time.Millisecond
	}
	if config.StreamPing <= 0 {
		config.StreamPing = 15 * time.Second
	}
	known := make(map[loopd.ResponderRef]struct{}, len(config.Responders))
	for _, responder := range config.Responders {
		known[responder.ResponderRef] = struct{}{}
	}
	return &Server{
		store: store, responders: append([]loopd.Responder(nil), config.Responders...), known: known,
		runner: NewRunner(store, config.Adapters, config.Logger, config.Runner), logger: config.Logger,
		streamPoll: config.StreamPoll, streamPing: config.StreamPing,
	}
}

func (server *Server) Run(ctx context.Context) {
	server.runner.Run(ctx)
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
	engine.GET("/v1/invocations/:invocation_id", server.adapt(server.getInvocation))
	engine.GET("/v1/invocations/:invocation_id/context", server.adapt(server.getInvocationContext))
	engine.GET("/v1/invocations/:invocation_id/events/stream", server.adapt(server.streamInvocation))
	engine.GET("/v1/operators/:operator_id/invocations", server.adapt(server.listOperatorInvocations))
	engine.GET("/v1/operators/:operator_id/events", server.adapt(server.listOperatorEvents))
	engine.POST("/v1/invocations/:invocation_id/accept", server.adapt(server.acceptInvocation))
	engine.POST("/v1/invocations/:invocation_id/reply", server.adapt(server.replyInvocation))
	engine.POST("/v1/invocations/:invocation_id/activities", server.adapt(server.upsertActivity))
	engine.POST("/v1/invocations/:invocation_id/harness-calls", server.adapt(server.promptHarness))
	engine.GET("/v1/harness-calls/:call_id", server.adapt(server.getHarnessCall))
	engine.GET("/v1/harness-calls/:call_id/events", server.adapt(server.listHarnessEvents))
	engine.POST("/v1/invocations/:invocation_id/interactions", server.adapt(server.createInteraction))
	engine.GET("/v1/interactions/:interaction_id", server.adapt(server.getInteraction))
	engine.POST("/v1/interactions/:interaction_id/resolve", server.adapt(server.resolveInteraction))
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
	case errors.Is(err, ErrInvalid):
		status, typeName = consts.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrNotFound):
		status, typeName = consts.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, typeName = consts.StatusConflict, "conflict"
	case errors.Is(err, ErrUnavailable):
		status, typeName = consts.StatusServiceUnavailable, "unavailable"
	default:
		server.logger.Error("loop-server request failed", "error", err)
	}
	request.JSON(status, errorResponse{Error: apiError{Type: typeName, Message: err.Error()}})
}

func (server *Server) listResponders(_ context.Context, request *hertzapp.RequestContext) error {
	request.JSON(consts.StatusOK, page[loopd.Responder]{Data: server.responders})
	return nil
}

func (server *Server) createConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input createConversationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	conversation, err := server.store.CreateConversation(ctx, input.Title)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusCreated, conversation)
	return nil
}

func (server *Server) getConversation(ctx context.Context, request *hertzapp.RequestContext) error {
	conversation, err := server.store.GetConversation(ctx, request.Param("conversation_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, conversation)
	return nil
}

func (server *Server) listMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	after, err := queryInt64(request, "after", 0)
	if err != nil {
		return err
	}
	limit, err := queryInt(request, "limit", defaultPageSize)
	if err != nil {
		return err
	}
	messages, err := server.store.ListMessages(ctx, request.Param("conversation_id"), after, 0, limit)
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
	if _, exists := server.known[input.Responder]; !exists {
		return fmt.Errorf("%w: responder %s/%s is not configured", ErrInvalid, input.Responder.Role, input.Responder.ID)
	}
	result, err := server.store.CreateMessageInvocation(ctx, request.Param("conversation_id"), input)
	if err != nil {
		return err
	}
	if input.Responder.Role == loopd.RoleHarness {
		_, _, err = server.store.EnsureHarnessCall(ctx, result.Invocation.ID, promptRequest{
			OwnerUID: result.Invocation.ID, EffectKey: "answer", Target: input.Responder.ID, Prompt: input.Content,
		})
		if err != nil {
			_ = server.store.FailInvocation(ctx, result.Invocation.ID, err.Error())
			return err
		}
		result.Invocation, err = server.store.StartInvocation(ctx, result.Invocation.ID)
		if err != nil {
			return err
		}
		server.runner.Notify()
	}
	request.JSON(consts.StatusAccepted, result)
	return nil
}

func (server *Server) getInvocation(ctx context.Context, request *hertzapp.RequestContext) error {
	invocation, err := server.store.GetInvocation(ctx, request.Param("invocation_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, invocation)
	return nil
}

func (server *Server) getInvocationContext(ctx context.Context, request *hertzapp.RequestContext) error {
	invocationContext, err := server.store.GetInvocationContext(ctx, request.Param("invocation_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, invocationContext)
	return nil
}

func (server *Server) listOperatorInvocations(ctx context.Context, request *hertzapp.RequestContext) error {
	limit, err := queryInt(request, "limit", defaultPageSize)
	if err != nil {
		return err
	}
	invocations, err := server.store.ListPendingInvocations(ctx, request.Param("operator_id"), limit)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.Invocation]{Data: invocations})
	return nil
}

func (server *Server) listOperatorEvents(ctx context.Context, request *hertzapp.RequestContext) error {
	after, err := queryUint64(request, "after", 0)
	if err != nil {
		return err
	}
	limit, err := queryInt(request, "limit", defaultPageSize)
	if err != nil {
		return err
	}
	events, err := server.store.ListOperatorEvents(ctx, request.Param("operator_id"), after, limit)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.OperatorEvent]{Data: events})
	return nil
}

func (server *Server) acceptInvocation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input acceptInvocationRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	invocation, err := server.store.AcceptInvocation(ctx, request.Param("invocation_id"), input.Resource)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, invocation)
	return nil
}

func (server *Server) replyInvocation(ctx context.Context, request *hertzapp.RequestContext) error {
	var input replyRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	invocation, err := server.store.GetInvocation(ctx, request.Param("invocation_id"))
	if err != nil {
		return err
	}
	if invocation.Responder.Role != loopd.RoleOperator {
		return fmt.Errorf("%w: only an Operator can reply through this endpoint", ErrConflict)
	}
	invocation, err = server.store.CompleteInvocation(ctx, invocation.ID, loopd.RoleOperator, invocation.Responder.ID, input.Content)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, invocation)
	return nil
}

func (server *Server) upsertActivity(ctx context.Context, request *hertzapp.RequestContext) error {
	var input loopd.ActivityUpdate
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	activity, err := server.store.UpsertActivity(ctx, request.Param("invocation_id"), input)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, activity)
	return nil
}

func (server *Server) promptHarness(ctx context.Context, request *hertzapp.RequestContext) error {
	var input promptRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	call, created, err := server.store.EnsureHarnessCall(ctx, request.Param("invocation_id"), input)
	if err != nil {
		return err
	}
	server.runner.Notify()
	status := consts.StatusOK
	if created {
		status = consts.StatusAccepted
	}
	request.JSON(status, call)
	return nil
}

func (server *Server) getHarnessCall(ctx context.Context, request *hertzapp.RequestContext) error {
	call, err := server.store.GetHarnessCall(ctx, request.Param("call_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, call)
	return nil
}

func (server *Server) listHarnessEvents(ctx context.Context, request *hertzapp.RequestContext) error {
	after, err := queryUint64(request, "after", 0)
	if err != nil {
		return err
	}
	limit, err := queryInt(request, "limit", defaultPageSize)
	if err != nil {
		return err
	}
	events, err := server.store.ListHarnessEvents(ctx, request.Param("call_id"), after, limit)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, page[loopd.HarnessEvent]{Data: events})
	return nil
}

func (server *Server) createInteraction(ctx context.Context, request *hertzapp.RequestContext) error {
	var input interactionRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	interaction, created, err := server.store.EnsureInteraction(ctx, request.Param("invocation_id"), input)
	if err != nil {
		return err
	}
	status := consts.StatusOK
	if created {
		status = consts.StatusCreated
	}
	request.JSON(status, interaction)
	return nil
}

func (server *Server) getInteraction(ctx context.Context, request *hertzapp.RequestContext) error {
	interaction, err := server.store.GetInteraction(ctx, request.Param("interaction_id"))
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, interaction)
	return nil
}

func (server *Server) resolveInteraction(ctx context.Context, request *hertzapp.RequestContext) error {
	var input resolveInteractionRequest
	if err := decodeBody(request, &input); err != nil {
		return err
	}
	interaction, err := server.store.ResolveInteraction(ctx, request.Param("interaction_id"), input.Answer)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, interaction)
	return nil
}

func decodeBody(request *hertzapp.RequestContext, target any) error {
	if len(request.Request.Body()) == 0 {
		return fmt.Errorf("%w: request body is required", ErrInvalid)
	}
	if err := json.Unmarshal(request.Request.Body(), target); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalid, err)
	}
	return nil
}

func queryInt(request *hertzapp.RequestContext, name string, fallback int) (int, error) {
	value := request.Query(name)
	if value == "" {
		return fallback, nil
	}
	result, err := strconv.Atoi(value)
	if err != nil || result < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", ErrInvalid, name)
	}
	return result, nil
}

func queryInt64(request *hertzapp.RequestContext, name string, fallback int64) (int64, error) {
	value := request.Query(name)
	if value == "" {
		return fallback, nil
	}
	result, err := strconv.ParseInt(value, 10, 64)
	if err != nil || result < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", ErrInvalid, name)
	}
	return result, nil
}

func queryUint64(request *hertzapp.RequestContext, name string, fallback uint64) (uint64, error) {
	value := request.Query(name)
	if value == "" {
		return fallback, nil
	}
	result, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", ErrInvalid, name)
	}
	return result, nil
}
