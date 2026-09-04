package api

import (
	"encoding/json"

	loopd "github.com/compforge/loopd"
)

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type page[T any] struct {
	Data []T `json:"data"`
}

type createConversationRequest struct {
	Name            string `json:"name,omitempty"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
}

type createChatMessagesRequest struct {
	UserKey   string             `json:"user_key"`
	Responder loopd.ResponderRef `json:"responder"`
	Content   json.RawMessage    `json:"content"`
}

type updateMessageContentRequest struct {
	Content json.RawMessage `json:"content"`
}
