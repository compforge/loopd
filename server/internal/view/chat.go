package view

import (
	"encoding/json"

	"github.com/compforge/loopd/pkg/contract"
)

type CreateChatMessagesRequest struct {
	TaskID  string            `json:"task_id,omitempty"`
	UserKey string            `json:"user_key,omitempty"`
	Target  contract.ActorRef `json:"target,omitempty"`
	Content json.RawMessage   `json:"content,omitempty"`
}
