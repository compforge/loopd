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
	ID              string     `json:"id"`
	InvocationID    string     `json:"invocation_id"`
	OwnerUID        string     `json:"owner_uid"`
	EffectKey       string     `json:"effect_key"`
	Target          string     `json:"target"`
	Phase           CallPhase  `json:"phase"`
	ExternalRef     string     `json:"external_ref,omitempty"`
	ProviderCursor  string     `json:"provider_cursor,omitempty"`
	LastEventCursor uint64     `json:"last_event_cursor,omitempty"`
	StreamText      string     `json:"stream_text,omitempty"`
	Result          string     `json:"result,omitempty"`
	Error           string     `json:"error,omitempty"`
	LastActivityAt  *time.Time `json:"last_activity_at,omitempty"`
	Timestamped
}

type HarnessEvent struct {
	Cursor         uint64          `json:"cursor"`
	ProviderCursor string          `json:"provider_cursor,omitempty"`
	CallID         string          `json:"call_id"`
	InvocationID   string          `json:"invocation_id"`
	Kind           string          `json:"kind"`
	Data           json.RawMessage `json:"data"`
	CreatedAt      time.Time       `json:"created_at"`
}
