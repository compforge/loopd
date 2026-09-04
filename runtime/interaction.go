package runtime

import (
	"context"
	"net/http"
	"net/url"
	"time"

	loopd "github.com/compforge/loopd"
)

type InteractionPrompt struct {
	InvocationID string
	OwnerUID     string
	EffectKey    string
	Requester    loopd.ActorRef
	Title        string
	Text         string
	Options      []loopd.InteractionOption
	ExpiresAt    *time.Time
}

func (loop Loop) Ask(ctx context.Context, prompt InteractionPrompt) (*Interaction, error) {
	return loop.interact(ctx, loopd.InteractionAsk, prompt)
}

func (loop Loop) Confirm(ctx context.Context, prompt InteractionPrompt) (*Interaction, error) {
	return loop.interact(ctx, loopd.InteractionConfirm, prompt)
}

func (loop Loop) interact(ctx context.Context, kind loopd.InteractionKind, prompt InteractionPrompt) (*Interaction, error) {
	var result loopd.Interaction
	err := loop.client.do(ctx, http.MethodPost,
		"/v1/invocations/"+url.PathEscape(prompt.InvocationID)+"/interactions",
		interactionRequest{
			OwnerUID: prompt.OwnerUID, EffectKey: prompt.EffectKey, Requester: prompt.Requester,
			Kind: kind, Title: prompt.Title, Prompt: prompt.Text, Options: prompt.Options, ExpiresAt: prompt.ExpiresAt,
		}, &result)
	if err != nil {
		return nil, err
	}
	return &Interaction{client: loop.client, value: result}, nil
}

type Interaction struct {
	client *client
	value  loopd.Interaction
}

func (interaction *Interaction) Value() loopd.Interaction { return interaction.value }

func (interaction *Interaction) Refresh(ctx context.Context) (loopd.Interaction, error) {
	var result loopd.Interaction
	err := interaction.client.do(ctx, http.MethodGet,
		"/v1/interactions/"+url.PathEscape(interaction.value.ID), nil, &result)
	if err == nil {
		interaction.value = result
	}
	return result, err
}

func (interaction *Interaction) Wait(ctx context.Context) (loopd.Interaction, error) {
	if interaction.value.Phase.Terminal() {
		return interaction.value, nil
	}
	ticker := time.NewTicker(interaction.client.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return interaction.value, ctx.Err()
		case <-ticker.C:
			value, err := interaction.Refresh(ctx)
			if err != nil {
				return interaction.value, err
			}
			if value.Phase.Terminal() {
				return value, nil
			}
		}
	}
}
