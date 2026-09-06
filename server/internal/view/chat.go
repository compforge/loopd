package view

import (
	"encoding/json"

	"github.com/compforge/loopd/pkg/contract"
)

type CreateChatMessagesRequest struct {
	UserKey string            `json:"user_key,omitempty"`
	Target  contract.ActorRef `json:"target,omitempty"`
	Content json.RawMessage   `json:"content,omitempty"`
}
