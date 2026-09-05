package loopd

import (
	"encoding/json"
)

type Conversation struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	ParentMessageID string `json:"parent_message_id,omitempty"`
	Timestamped
}

type Message struct {
	ReplyToMessageID string          `json:"reply_to_message_id,omitempty"`
	Purpose          string          `json:"purpose,omitempty"`
	Revision         uint64          `json:"revision,omitempty"`
	ID               string          `json:"id"`
	ConversationID   string          `json:"conversation_id"`
	TaskID           string          `json:"task_id"`
	Kind             Role            `json:"kind"`
	Key              string          `json:"key"`
	Content          json.RawMessage `json:"content"`
	Timestamped
}
