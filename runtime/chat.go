package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/compforge/loopd/api"
)

type Chat struct {
	client *client
}

func (chat Chat) Context(ctx context.Context, invocationID string) (api.InvocationContext, error) {
	var result api.InvocationContext
	err := chat.client.do(ctx, http.MethodGet, "/v1/invocations/"+url.PathEscape(invocationID)+"/context", nil, &result)
	return result, err
}

func (chat Chat) History(ctx context.Context, conversationID string, after int64, limit int) ([]api.Message, error) {
	var result api.Page[api.Message]
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages?after=" + strconv.FormatInt(after, 10) +
		"&limit=" + strconv.Itoa(limit)
	err := chat.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

func (chat Chat) Reply(ctx context.Context, invocationID, content string) (api.Invocation, error) {
	var result api.Invocation
	err := chat.client.do(ctx, http.MethodPost, "/v1/invocations/"+url.PathEscape(invocationID)+"/reply",
		api.ReplyRequest{Content: content}, &result)
	return result, err
}

func (chat Chat) Activity(ctx context.Context, invocationID string, request api.ActivityRequest) (api.Activity, error) {
	var result api.Activity
	err := chat.client.do(ctx, http.MethodPost, "/v1/invocations/"+url.PathEscape(invocationID)+"/activities", request, &result)
	return result, err
}
