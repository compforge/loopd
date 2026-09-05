package loopd

import (
	"encoding/json"
	"time"
)

type CallPhase string

const (
	CallPending      CallPhase = "pending"
	CallStarting     CallPhase = "starting"
	CallRunning      CallPhase = "running"
	CallWaitingInput CallPhase = "waiting_input"
	CallSucceeded    CallPhase = "succeeded"
	CallFailed       CallPhase = "failed"
	CallCancelled    CallPhase = "cancelled"
	CallUnknown      CallPhase = "unknown"
)

func (phase CallPhase) Terminal() bool {
	switch phase {
	case CallSucceeded, CallFailed, CallCancelled, CallUnknown:
		return true
	default:
		return false
	}
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type HarnessCall struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"task_id"`
	EffectKey      string     `json:"effect_key"`
	Target         string     `json:"target"`
	Phase          CallPhase  `json:"phase"`
	Result         string     `json:"result,omitempty"`
	Error          string     `json:"error,omitempty"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
	Timestamped
}

// Event is one event accepted by loop-server's delivery stream. ID is the
// transport event identity used by SSE Last-Event-ID; Data is an AgentUE event
// whose seq owns semantic ordering and publish idempotency.
type Event struct {
	MessageID string          `json:"message_id,omitempty"`
	Message   *Message        `json:"message,omitempty"`
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
}
