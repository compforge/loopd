package view

import (
	"encoding/json"

	"github.com/compforge/loopd/pkg/contract"
)

type CreateMessageRequest struct {
	contract.SpeakRequest
	UserKey string `json:"user_key,omitempty"`
}

type MessageEventRequest struct {
	Event  json.RawMessage        `json:"event"`
	Status contract.MessageStatus `json:"status,omitempty"`
}
type MessageEventResponse struct {
	ID string `json:"id"`
}
type MessageEvent struct {
	Message *contract.Message `json:"message,omitempty"`
	Event   json.RawMessage   `json:"event"`
}
