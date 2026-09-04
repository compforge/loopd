package runtime

import (
	"time"

	"github.com/compforge/loopd"
)

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type page[T any] struct {
	Data []T `json:"data"`
}

type acceptInvocationRequest struct {
	Resource loopd.ResourceRef `json:"resource"`
}

type interactionRequest struct {
	OwnerUID  string                    `json:"owner_uid"`
	EffectKey string                    `json:"effect_key"`
	Requester loopd.ActorRef            `json:"requester"`
	Kind      loopd.InteractionKind     `json:"kind"`
	Title     string                    `json:"title,omitempty"`
	Prompt    string                    `json:"prompt"`
	Options   []loopd.InteractionOption `json:"options,omitempty"`
	ExpiresAt *time.Time                `json:"expires_at,omitempty"`
}
