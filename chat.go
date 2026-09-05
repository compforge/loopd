package loopd

import (
	"encoding/json"
)

type Conversation struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Actor identifies the conversation's organizer, not every message sender.
	ActorKind Role   `json:"actor_kind"`
	ActorKey  string `json:"actor_key"`
	TaskID    string `json:"task_id,omitempty"`
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

// OutputRequest identifies one independently published message in the Task's work conversation.
// Key is stable within the Task; separate outputs by the same actor use different keys.
type OutputRequest struct {
	Key   string   `json:"key"`
	Actor ActorRef `json:"actor"`
}
