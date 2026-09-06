package view

import (
	"encoding/json"

	loopd "github.com/compforge/loopd"
)

// Message is a page projection. Cards and references are never persisted.
type Message struct {
	loopd.Message
	Card    MessageCard       `json:"card"`
	ReplyTo *MessageReference `json:"reply_to,omitempty"`
}
type MessageReference struct {
	ID      string          `json:"id"`
	Kind    loopd.ActorKind `json:"kind"`
	Key     string          `json:"key"`
	Preview string          `json:"preview"`
}
type MessageCard struct {
	Type          string            `json:"type"`
	Mode          string            `json:"mode,omitempty"`
	QuestionID    string            `json:"question_id,omitempty"`
	Question      *loopd.HumanBlock `json:"question,omitempty"`
	SelectedValue *string           `json:"selected_value,omitempty"`
	ReplyID       string            `json:"reply_id,omitempty"`
	Editable      bool              `json:"editable"`
}

type MessageEventRequest struct {
	Event  json.RawMessage     `json:"event"`
	Status loopd.MessageStatus `json:"status,omitempty"`
}
type MessageEventResponse struct {
	ID string `json:"id"`
}
type MessageEvent struct {
	MessageID string          `json:"message_id"`
	Message   *Message        `json:"message,omitempty"`
	Event     json.RawMessage `json:"event"`
}
