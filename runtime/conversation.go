package runtime

import (
	"context"
	loopd "github.com/compforge/loopd"
	"net/http"
	"net/url"
)

// Conv exposes the persistent collaboration boundary to Operators.
type Conv struct{ client *client }

// Poll is a write Verb recording receipt without committing consumption.
// Pass the last successful result's Position as After while working.
// On recovery omit After to replay inputs after the committed position.
func (conv Conv) Poll(ctx context.Context, conversationID string, request loopd.PollRequest) (loopd.PollResult, error) {
	var result loopd.PollResult
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/poll", request, &result)
	return result, err
}

// Commit is a write Verb acknowledging a contiguous safely handled prefix.
// This does not complete business work, close streams, or delete the Conv.
func (conv Conv) Commit(ctx context.Context, conversationID string, request loopd.CommitRequest) error {
	return conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/commit", request, nil)
}

// Read is a read Verb; shared history neither changes consumption nor wakes actors.
func (conv Conv) Read(ctx context.Context, conversationID, after string, limit int) ([]loopd.Message, error) {
	return (Delivery{client: conv.client}).History(ctx, conversationID, after, limit)
}

// Speak is a write Verb creating or reusing an actor's message in a conversation.
// It does not require a user input or an open UI delivery.
func (conv Conv) Speak(ctx context.Context, conversationID string, request loopd.SpeakRequest) (loopd.Message, error) {
	var result loopd.Message
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/speak", request, &result)
	return result, err
}

// Workspace is a write Verb lazily reusing this actor's internal conversation.
func (conv Conv) Workspace(ctx context.Context, conversationID string, actor loopd.ActorRef) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/actors", actor, &result)
	return result, err
}
