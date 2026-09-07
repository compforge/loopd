package view

import (
	"encoding/json"

	"github.com/compforge/loopd/pkg/contract"
)

type MessageEventRequest struct {
	Event  json.RawMessage        `json:"event"`
	Status contract.MessageStatus `json:"status,omitempty"`
}
type MessageEventResponse struct {
	ID string `json:"id"`
}
type MessageEvent struct {
	MessageID string            `json:"message_id"`
	Message   *contract.Message `json:"message,omitempty"`
	Event     json.RawMessage   `json:"event"`
}
