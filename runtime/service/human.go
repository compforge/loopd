package service

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
)

type Human struct{ client *infra.Client }

func NewHuman(c *infra.Client) Human { return Human{client: c} }

type HumanHandle struct {
	client    *infra.Client
	messageID string
}

func (h *HumanHandle) ID() string { return h.messageID }

func (h Human) Create(ctx context.Context, r contract.HumanRequest) (*HumanHandle, error) {
	if err := r.Validate(); err != nil {
		return nil, model.WrapError(err)
	}
	var result contract.HumanResult
	path := "/v1/conversations/" + url.PathEscape(r.ConversationID) + "/human"
	if err := h.client.Do(ctx, http.MethodPost, path, r, &result); err != nil {
		return nil, err
	}
	return &HumanHandle{client: h.client, messageID: result.Message.ID}, nil
}

// Get is a Verb (effect: read) observing the authoritative question state.
func (h *HumanHandle) Get(ctx context.Context) (contract.HumanResult, error) {
	var result contract.HumanResult
	err := h.client.Do(ctx, http.MethodGet, "/v1/human/"+url.PathEscape(h.messageID), nil, &result)
	return result, err
}

// Wait is a Verb (effect: read); it only observes. Cancelling ctx never dismisses the persisted request.
func (h *HumanHandle) Wait(ctx context.Context) (contract.HumanResult, error) {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		result, err := h.Get(ctx)
		if err != nil || result.Status.Terminal() {
			return result, err
		}
		select {
		case <-ctx.Done():
			return contract.HumanResult{}, model.WrapError(ctx.Err())
		case <-timer.C:
		}
	}
}

// Get observes a persisted question after an Operator restart.
func (h Human) Get(ctx context.Context, messageID string) (contract.HumanResult, error) {
	return (&HumanHandle{client: h.client, messageID: messageID}).Get(ctx)
}
