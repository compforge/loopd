package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	hertzapp "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/service"
	"github.com/compforge/loopd/server/internal/view"
)

const defaultPageSize = 100

func (server *Server) listMessages(ctx context.Context, request *hertzapp.RequestContext) error {
	limit, err := queryLimit(request)
	if err != nil {
		return err
	}
	var messages []contract.Message
	if watch := request.Query("watch"); watch != "" {
		revisions, parseErr := parseMessageWatch(watch)
		if parseErr != nil {
			return parseErr
		}
		messages, err = server.messages.MessageChanges(ctx, request.Param("conversation_id"), revisions)
	} else {
		query := contract.MessageQuery{After: request.Query("after"), Before: request.Query("before"), Order: contract.MessageOrder(request.Query("order")), Limit: limit}
		if ids := request.Query("ids"); ids != "" {
			query.IDs = strings.Split(ids, ",")
		}
		for _, status := range request.QueryArgs().PeekAll("status") {
			query.Statuses = append(query.Statuses, contract.MessageStatus(status))
		}
		page, queryErr := server.messages.QueryMessages(ctx, request.Param("conversation_id"), query)
		if queryErr != nil {
			return queryErr
		}
		request.JSON(consts.StatusOK, page)
		return nil
	}
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, view.Page[contract.Message]{Data: messages})
	return nil
}

// watch is a bounded set of known active message IDs and their last revisions.
// It does not advance the independent new-message discovery cursor.
func parseMessageWatch(raw string) (map[string]uint64, error) {
	items := strings.Split(raw, ",")
	if len(items) > 100 {
		return nil, service.ErrInvalid
	}
	result := make(map[string]uint64, len(items))
	for _, item := range items {
		id, revision, ok := strings.Cut(item, ":")
		value, err := strconv.ParseUint(revision, 10, 64)
		if !ok || id == "" || err != nil {
			return nil, service.ErrInvalid
		}
		result[id] = value
	}
	return result, nil
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

func (s *Server) getMessageInfo(ctx context.Context, r *hertzapp.RequestContext) error {
	value, err := s.messages.MessageInfo(ctx, r.Param("conversation_id"), r.Param("message_id"))
	if err != nil {
		return err
	}
	r.JSON(200, value)
	return nil
}
func (s *Server) getMessageContent(ctx context.Context, r *hertzapp.RequestContext) error {
	value, err := s.messages.MessageSnapshot(ctx, r.Param("conversation_id"), r.Param("message_id"))
	if err != nil {
		return err
	}
	r.JSON(200, value)
	return nil
}
func (s *Server) getMessageBlocks(ctx context.Context, r *hertzapp.RequestContext) error {
	page, err := s.messages.MessageBlocks(ctx, r.Param("conversation_id"), r.Param("message_id"), r.Param("block_id"), r.Query("cursor"))
	if err != nil {
		return err
	}
	if r.Param("block_id") != "" {
		r.JSON(200, contract.BlockSnapshot{Revision: page.Revision, Block: page.Data[0]})
	} else {
		r.JSON(200, page)
	}
	return nil
}
