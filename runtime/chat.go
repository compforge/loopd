package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	loopd "github.com/compforge/loopd"
)

type Chat struct {
	client *client
}

type CreateConversationRequest struct {
	Name            string `json:"name"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
}

type SendMessageRequest struct {
	UserKey   string             `json:"user_key"`
	Responder loopd.ResponderRef `json:"responder"`
	Content   json.RawMessage    `json:"content"`
}

func (chat Chat) CreateConversation(
	ctx context.Context,
	request CreateConversationRequest,
) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := chat.client.do(ctx, http.MethodPost, "/v1/conversations", request, &result)
	return result, err
}

func (chat Chat) Conversation(ctx context.Context, conversationID string) (loopd.Conversation, error) {
	var result loopd.Conversation
	err := chat.client.do(
		ctx,
		http.MethodGet,
		"/v1/conversations/"+url.PathEscape(conversationID),
		nil,
		&result,
	)
	return result, err
}

func (chat Chat) Send(
	ctx context.Context,
	conversationID string,
	request SendMessageRequest,
) (loopd.Message, error) {
	var result loopd.Message
	err := chat.client.do(
		ctx,
		http.MethodPost,
		"/v1/conversations/"+url.PathEscape(conversationID)+"/messages",
		request,
		&result,
	)
	return result, err
}

func (chat Chat) History(
	ctx context.Context,
	conversationID string,
	after string,
	limit int,
) ([]loopd.Message, error) {
	var result page[loopd.Message]
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages?after=" + url.QueryEscape(after) +
		"&limit=" + strconv.Itoa(limit)
	err := chat.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

func (chat Chat) Update(
	ctx context.Context,
	conversationID string,
	messageID string,
	content json.RawMessage,
) (loopd.Message, error) {
	var result loopd.Message
	err := chat.client.do(
		ctx,
		http.MethodPut,
		"/v1/conversations/"+url.PathEscape(conversationID)+"/messages/"+url.PathEscape(messageID)+"/content",
		struct {
			Content json.RawMessage `json:"content"`
		}{Content: content},
		&result,
	)
	return result, err
}
