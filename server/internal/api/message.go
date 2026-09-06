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
		messages, err = server.messages.ListMessages(ctx, request.Param("conversation_id"), request.Query("after"), limit)
	}
	if err != nil {
		return err
	}
	views, err := server.messages.EnrichMessages(ctx, messages)
	if err != nil {
		return err
	}
	request.JSON(consts.StatusOK, view.Page[view.Message]{Data: views})
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
