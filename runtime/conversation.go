package runtime

import (
	"context"
	"net/http"
	"net/url"

	"github.com/compforge/loopd/pkg/contract"
)

// Conv exposes the persistent collaboration boundary to Operators.
type Conv struct {
	client *client
}

// Poll is a write Verb recording receipt without committing consumption.
// Pass the last successful result's Position as After while working.
// On recovery omit After to replay inputs after the committed position.
func (conv Conv) Poll(ctx context.Context, conversationID string, request contract.PollRequest) (contract.PollResult, error) {
	var result contract.PollResult
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/poll", request, &result)
	return result, err
}

// Commit is a write Verb acknowledging a contiguous safely handled prefix.
// This does not complete business work, close streams, or delete the Conv.
func (conv Conv) Commit(ctx context.Context, conversationID string, request contract.CommitRequest) error {
	return conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/commit", request, nil)
}

// Speak is a write Verb creating or reusing an actor's message in a conversation.
// Content is complete; Status may mark failure. Use Tell for incremental output.
func (conv Conv) Speak(ctx context.Context, conversationID string, request contract.SpeakRequest) (Message, error) {
	if request.Status == "" {
		request.Status = contract.MessageStatusCompleted
	}
	if !request.Status.Terminal() {
		return nil, errSpeakStatusInvalid
	}
	var result contract.MessageInfo
	err := conv.client.write(ctx, "/v1/conversations/"+url.PathEscape(conversationID)+"/messages", request, &result)
	if err != nil {
		return nil, err
	}
	return conv.Read(conversationID, result.ID), nil
}

// Tell starts or restores one streaming message. Only its writer may Emit/End.
func (conv Conv) Tell(ctx context.Context, conversationID string, request contract.SpeakRequest) (Stream, error) {
	if request.Status != "" && request.Status != contract.MessageStatusStreaming {
		return nil, errTellStatusInvalid
	}
	request.Status = contract.MessageStatusStreaming
	var result contract.MessageInfo
	err := conv.client.write(ctx, "/v1/conversations/"+url.PathEscape(conversationID)+"/messages", request, &result)
	if err != nil {
		return nil, err
	}
	result.ConversationID = conversationID
	return newMessageStream(conv.client, result), nil
}
