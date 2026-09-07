package model

import (
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

type AskRequest struct {
	ConversationID string
	Actor          contract.ActorRef
	Target         contract.ActorRef
	ReplyToID      string
	EffectKey      string
	Title          string
	Prompt         string
	Timeout        time.Duration
	Choices        []contract.HumanChoice
	AllowOther     bool
}
type ConfirmRequest struct {
	ConversationID string
	Actor          contract.ActorRef
	Target         contract.ActorRef
	ReplyToID      string
	EffectKey      string
	Title          string
	Prompt         string
	Timeout        time.Duration
	ConfirmLabel   string
	DeclineLabel   string
}
