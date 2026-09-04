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

type createMessageRequest struct {
	Kind    loopd.Role      `json:"kind"`
	Key     string          `json:"key"`
	Content json.RawMessage `json:"content"`
}
