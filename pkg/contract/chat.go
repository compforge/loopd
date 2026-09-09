package contract

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

type MessageStatus string

const (
	MessageStatusStreaming MessageStatus = "streaming"
	MessageStatusCompleted MessageStatus = "completed"
	MessageStatusFailed    MessageStatus = "failed"
	MessageStatusCancelled MessageStatus = "cancelled"
	MessageStatusExpired   MessageStatus = "expired"
)

func (status MessageStatus) Terminal() bool {
	return status == MessageStatusCompleted || status == MessageStatusFailed || status == MessageStatusCancelled || status == MessageStatusExpired
}

type Message struct {
	Status         MessageStatus   `json:"status"`
	TargetKind     ActorKind       `json:"target_kind,omitempty"`
	TargetKey      string          `json:"target_key,omitempty"`
	ReplyToID      string          `json:"reply_to_id,omitempty"`
	Revision       uint64          `json:"revision,omitempty"`
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	TaskID         string          `json:"task_id"`
	SourceKind     ActorKind       `json:"source_kind"`
	SourceKey      string          `json:"source_key"`
	Content        json.RawMessage `json:"content"`
	Timestamped
}

// SpeakRequest creates one actor-owned message. Key is stable within the
// conversation and actor, independent of any UI delivery. Empty Target broadcasts.
type SpeakRequest struct {
	// Status selects the initial lifecycle; empty means completed.
	// Streaming messages accept later events, including End with a terminal status.
	Status    MessageStatus   `json:"status,omitempty"`
	Key       string          `json:"key"`
	Actor     ActorRef        `json:"actor"`
	Target    ActorRef        `json:"target,omitempty"`
	ReplyToID string          `json:"reply_to_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// Ended reports whether the message writer has finished sending. It says nothing
// about the conversation, the consumer position, or any business execution.
func (message Message) Ended() bool {
	return message.Status.Terminal()
}
