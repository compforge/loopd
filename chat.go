package loopd

import (
	"encoding/json"
)

type Conversation struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Actor identifies the conversation's organizer, not every message sender.
	ActorKind ActorKind `json:"actor_kind"`
	ActorKey  string    `json:"actor_key"`
	ParentID  string    `json:"parent_id,omitempty"`
	Timestamped
}

type Message struct {
	TargetKind     ActorKind       `json:"target_kind,omitempty"`
	TargetKey      string          `json:"target_key,omitempty"`
	ReplyToID      string          `json:"reply_to_id,omitempty"`
	Purpose        string          `json:"purpose,omitempty"`
	Revision       uint64          `json:"revision,omitempty"`
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	TaskID         string          `json:"task_id"`
	Kind           ActorKind       `json:"kind"`
	Key            string          `json:"key"`
	Content        json.RawMessage `json:"content"`
	Timestamped
}

// SpeakRequest creates one actor-owned message. Key is stable within the
// conversation and actor, independent of any UI delivery. Empty Target broadcasts.
type SpeakRequest struct {
	// Stream leaves the message open for incremental output. The default publishes a complete message.
	Stream    bool            `json:"stream,omitempty"`
	Key       string          `json:"key"`
	Actor     ActorRef        `json:"actor"`
	Target    ActorRef        `json:"target,omitempty"`
	ReplyToID string          `json:"reply_to_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// Ended reports whether the message writer has finished sending. It says nothing
// about the conversation, the consumer position, or any business execution.
func (message Message) Ended() bool {
	var value struct {
		Meta struct {
			Output struct {
				Ended bool `json:"ended"`
			} `json:"output"`
		} `json:"meta"`
	}
	return json.Unmarshal(message.Content, &value) == nil && value.Meta.Output.Ended
}
