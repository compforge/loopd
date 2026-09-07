package verb

import (
	"context"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/model"
	"github.com/compforge/loopd/runtime/service"
)

type Human struct{ service service.Human }

func NewHuman(s service.Human) Human { return Human{service: s} }

// Ask is a Verb (effect: write) creating or reusing a durable question.
func (h Human) Ask(ctx context.Context, r model.AskRequest) (*service.HumanHandle, error) {
	return h.service.Create(ctx, contract.HumanRequest{ConversationID: r.ConversationID, Actor: r.Actor, Target: r.Target, ReplyToID: r.ReplyToID, EffectKey: r.EffectKey, Type: "ask", Title: r.Title, Prompt: r.Prompt, Timeout: r.Timeout, Choices: r.Choices, AllowOther: r.AllowOther})
}

// Confirm is a Verb (effect: write) creating or reusing a durable confirmation.
func (h Human) Confirm(ctx context.Context, r model.ConfirmRequest) (*service.HumanHandle, error) {
	return h.service.Create(ctx, contract.HumanRequest{ConversationID: r.ConversationID, Actor: r.Actor, Target: r.Target, ReplyToID: r.ReplyToID, EffectKey: r.EffectKey, Type: "confirm", Title: r.Title, Prompt: r.Prompt, Timeout: r.Timeout, ConfirmLabel: r.ConfirmLabel, DeclineLabel: r.DeclineLabel})
}

// Get observes a persisted question after an Operator restart.
func (h Human) Get(ctx context.Context, id string) (contract.HumanResult, error) {
	return h.service.Get(ctx, id)
}
