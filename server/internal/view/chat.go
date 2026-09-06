package view

import (
	"encoding/json"

	loopd "github.com/compforge/loopd"
)

type CreateChatMessagesRequest struct {
	TaskID  string          `json:"task_id,omitempty"`
	UserKey string          `json:"user_key,omitempty"`
	Target  loopd.ActorRef  `json:"target,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}
