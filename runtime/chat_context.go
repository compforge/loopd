package runtime

import (
	"context"
	loopd "github.com/compforge/loopd"
	"net/http"
	"net/url"
	"strconv"
)

// Context reads the UI Chat's input and available messages; it does not define
// an Operator business task. Conversation-wide input is received via Conv.Listen.
func (chat Chat) Context(ctx context.Context, taskID string) (loopd.ChatContext, error) {
	var result loopd.ChatContext
	err := chat.client.do(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), nil, &result)
	return result, err
}

func (chat Chat) Messages(ctx context.Context, taskID, after string, limit int) ([]loopd.Message, error) {
	var result page[loopd.Message]
	path := "/v1/tasks/" + url.PathEscape(taskID) + "/messages?after=" + url.QueryEscape(after) + "&limit=" + strconv.Itoa(limit)
	err := chat.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}
