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
	EffectKey      string     `json:"effect_key"`
	Target         string     `json:"target"`
	Phase          CallPhase  `json:"phase"`
	Result         string     `json:"result,omitempty"`
	Error          string     `json:"error,omitempty"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
	Timestamped
}
