package api

import "time"

type InteractionKind string

const (
	InteractionAsk     InteractionKind = "ask"
	InteractionConfirm InteractionKind = "confirm"
)

type InteractionPhase string

const (
	InteractionPending   InteractionPhase = "pending"
	InteractionResolved  InteractionPhase = "resolved"
	InteractionCancelled InteractionPhase = "cancelled"
	InteractionExpired   InteractionPhase = "expired"
)

func (phase InteractionPhase) Terminal() bool {
	return phase == InteractionResolved || phase == InteractionCancelled || phase == InteractionExpired
}

type InteractionOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Interaction struct {
	ID           string              `json:"id"`
	InvocationID string              `json:"invocation_id"`
	OwnerUID     string              `json:"owner_uid"`
	EffectKey    string              `json:"effect_key"`
	Requester    ResponderRef        `json:"requester"`
	Kind         InteractionKind     `json:"kind"`
	Title        string              `json:"title,omitempty"`
	Prompt       string              `json:"prompt"`
	Options      []InteractionOption `json:"options,omitempty"`
	Phase        InteractionPhase    `json:"phase"`
	Answer       string              `json:"answer,omitempty"`
	ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
	ResolvedAt   *time.Time          `json:"resolved_at,omitempty"`
	Timestamped
}

type InteractionRequest struct {
	OwnerUID  string              `json:"owner_uid"`
	EffectKey string              `json:"effect_key"`
	Requester ResponderRef        `json:"requester"`
	Kind      InteractionKind     `json:"kind"`
	Title     string              `json:"title,omitempty"`
	Prompt    string              `json:"prompt"`
	Options   []InteractionOption `json:"options,omitempty"`
	ExpiresAt *time.Time          `json:"expires_at,omitempty"`
}

type ResolveInteractionRequest struct {
	Answer string `json:"answer"`
}
