package service

import (
	"context"
	"net/http"
	"net/url"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
)

// Conv coordinates conversation reads, publications and consumption through Server APIs.
type Conv struct {
	client *infra.Client
}

// Poll is a write Verb recording receipt without committing consumption.
// Pass the last successful result's Position as After while working.
// On recovery omit After to replay inputs after the committed position.
func (conv Conv) Poll(ctx context.Context, conversationID string, request contract.PollRequest) (contract.PollResult, error) {
	var result contract.PollResult
	err := conv.client.Do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/poll", request, &result)
	return result, err
}

// Commit is a write Verb acknowledging a contiguous safely handled prefix.
// This does not complete business work, close streams, or delete the Conv.
func (conv Conv) Commit(ctx context.Context, conversationID string, request contract.CommitRequest) error {
	return conv.client.Do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/commit", request, nil)
}

func NewConv(c *infra.Client) Conv { return Conv{client: c} }

func (conv Conv) Create(ctx context.Context, conversationID string, request contract.SpeakRequest) (model.MessageInfo, error) {
	var result model.MessageInfo
	err := write(conv.client, ctx, "/v1/conversations/"+url.PathEscape(conversationID)+"/messages", request, &result)
	result.ConversationID = conversationID
	return result, err
}

func (conv Conv) Stream(info model.MessageInfo) model.Stream {
	return newMessageStream(conv.client, info)
}
