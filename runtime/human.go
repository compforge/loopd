package runtime

import (
	"context"
	"net/http"
	"net/url"
	"time"

	loopd "github.com/compforge/loopd"
)

type Human struct{ client *client }
type AskRequest struct {
	TaskID     string
	EffectKey  string
	Title      string
	Prompt     string
	Timeout    time.Duration
	Choices    []loopd.HumanChoice
	AllowOther bool
}
type ConfirmRequest struct {
	TaskID       string
	EffectKey    string
	Title        string
	Prompt       string
	Timeout      time.Duration
	ConfirmLabel string
	DeclineLabel string
}
type HumanHandle struct {
	client    *client
	messageID string
}

func (h *HumanHandle) ID() string { return h.messageID }
func (h Human) Ask(ctx context.Context, r AskRequest) (*HumanHandle, error) {
	return h.create(ctx, loopd.HumanRequest{TaskID: r.TaskID, EffectKey: r.EffectKey, Type: "ask", Title: r.Title, Prompt: r.Prompt, Timeout: r.Timeout, Choices: r.Choices, AllowOther: r.AllowOther})
}
func (h Human) Confirm(ctx context.Context, r ConfirmRequest) (*HumanHandle, error) {
	return h.create(ctx, loopd.HumanRequest{TaskID: r.TaskID, EffectKey: r.EffectKey, Type: "confirm", Title: r.Title, Prompt: r.Prompt, Timeout: r.Timeout, ConfirmLabel: r.ConfirmLabel, DeclineLabel: r.DeclineLabel})
}
func (h Human) create(ctx context.Context, r loopd.HumanRequest) (*HumanHandle, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	var result loopd.HumanResult
	if err := h.client.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(r.TaskID)+"/human", r, &result); err != nil {
		return nil, err
	}
	return &HumanHandle{client: h.client, messageID: result.Message.ID}, nil
}
func (h *HumanHandle) Get(ctx context.Context) (loopd.HumanResult, error) {
	var result loopd.HumanResult
	err := h.client.do(ctx, http.MethodGet, "/v1/human/"+url.PathEscape(h.messageID), nil, &result)
	return result, err
}

// Wait only observes. Cancelling ctx never dismisses the persisted request.
func (h *HumanHandle) Wait(ctx context.Context) (loopd.HumanResult, error) {
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		result, err := h.Get(ctx)
		if err != nil || result.Status.Terminal() {
			return result, err
		}
		select {
		case <-ctx.Done():
			return loopd.HumanResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}
