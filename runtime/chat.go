package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	loopd "github.com/compforge/loopd"
)

type Chat struct {
	client *client
}

func (chat Chat) Context(ctx context.Context, invocationID string) (loopd.InvocationContext, error) {
	var result loopd.InvocationContext
	err := chat.client.do(ctx, http.MethodGet, "/v1/invocations/"+url.PathEscape(invocationID)+"/context", nil, &result)
	return result, err
}

func (chat Chat) History(ctx context.Context, conversationID, after string, limit int) ([]loopd.Message, error) {
	var result page[loopd.Message]
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages?after=" + url.QueryEscape(after) +
		"&limit=" + strconv.Itoa(limit)
	err := chat.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

func (chat Chat) Reply(ctx context.Context, invocationID, content string) (loopd.Invocation, error) {
	var result loopd.Invocation
	err := chat.client.do(ctx, http.MethodPost, "/v1/invocations/"+url.PathEscape(invocationID)+"/reply",
		replyRequest{Content: content}, &result)
	return result, err
}

func (chat Chat) Activity(ctx context.Context, invocationID string, request loopd.ActivityUpdate) (loopd.Activity, error) {
	var result loopd.Activity
	err := chat.client.do(ctx, http.MethodPost, "/v1/invocations/"+url.PathEscape(invocationID)+"/activities", request, &result)
	return result, err
}
