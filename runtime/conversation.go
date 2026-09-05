package runtime

import (
	"context"
	loopd "github.com/compforge/loopd"
	"net/http"
	"net/url"
	"strconv"
)

// Conv exposes the persistent collaboration boundary to Operators.
type Conv struct {
	client   *client
	messages *messageHandles
}

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
	var result page[loopd.Message]
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages?after=" + url.QueryEscape(after) + "&limit=" + strconv.Itoa(limit)
	err := conv.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

// Speak is a write Verb creating or reusing an actor's message in a conversation.
// Content is complete by default. With Stream=true, use the returned handle to
// Emit incremental content and End the message. Neither mode owns a UI connection.
func (conv Conv) Speak(ctx context.Context, conversationID string, request loopd.SpeakRequest) (*Message, error) {
	var result loopd.Message
	err := conv.client.write(ctx, "/v1/conversations/"+url.PathEscape(conversationID)+"/speak", request, &result)
	if err != nil {
		return nil, err
	}
	return conv.messages.handle(conv.client, result), nil
}

// Workspace is a write Verb lazily reusing this actor's internal conversation.
func (conv Conv) Workspace(ctx context.Context, conversationID string, actor loopd.ActorRef) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := conv.client.do(ctx, http.MethodPost, "/v1/conversations/"+url.PathEscape(conversationID)+"/actors", actor, &result)
	return result, err
}
