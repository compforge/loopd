package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/runtime/infra"
	"github.com/compforge/loopd/runtime/model"
)

type Harness struct{ client *infra.Client }

func NewHarness(c *infra.Client) Harness { return Harness{client: c} }

// Prompt submits a durable call. The Server owns driving and persisting output;
// returning or closing a Runtime never ends remote execution.
func (h Harness) Prompt(ctx context.Context, p model.Prompt) (*Call, error) {
	var value contract.HarnessCall
	if err := write(h.client, ctx, "/v1/harness/runs", p, &value); err != nil {
		if model.IsConflict(err) {
			return nil, errors.Join(model.ErrCallConflict, err)
		}
		return nil, err
	}
	return &Call{client: h.client, id: value.ID, message: &remoteMessage{client: h.client, convID: p.ConversationID, id: value.MessageID}}, nil
}

// Call reconstructs a remote handle from a persisted run ID without starting work.
func (h Harness) Call(id string) *Call { return &Call{client: h.client, id: id} }

type Call struct {
	message model.Message
	client  *infra.Client
	id      string
}

// Message resolves the output reference. A restored Call knows only its Run ID,
// so its first resolution reads Run metadata; no message body is loaded.
func (c *Call) Message(ctx context.Context) (model.Message, error) {
	if c.message != nil {
		return c.message, nil
	}
	value, err := c.info(ctx)
	if err != nil {
		return nil, err
	}
	return &remoteMessage{client: c.client, convID: value.ConversationID, id: value.MessageID}, nil
}
func (c *Call) ID() string { return c.id }
func (c *Call) Get(ctx context.Context) (contract.HarnessCall, error) {
	value, err := c.info(ctx)
	if err != nil || value.Phase != contract.CallSucceeded || value.Result != nil {
		return value, err
	}
	message := &remoteMessage{client: c.client, convID: value.ConversationID, id: value.MessageID}
	result, err := message.Block(ctx, "result")
	if err != nil {
		return value, err
	}
	data, err := json.Marshal(struct {
		Blocks []json.RawMessage `json:"blocks"`
	}{Blocks: []json.RawMessage{result.Block}})
	if err != nil {
		return value, model.WrapError(err)
	}
	value.Result, err = contract.ExtractResult(data)
	if err != nil {
		return value, model.WrapError(err)
	}
	if value.Result == nil {
		return value, &model.Error{Message: "completed Harness call has no result block"}
	}
	return value, nil
}
func (c *Call) info(ctx context.Context) (contract.HarnessCall, error) {
	var value contract.HarnessCall
	err := c.client.Do(ctx, http.MethodGet, "/v1/harness/runs/"+url.PathEscape(c.id), nil, &value)
	return value, err
}
func (c *Call) Cancel(ctx context.Context) error {
	return write(c.client, ctx, "/v1/harness/runs/"+url.PathEscape(c.id)+"/cancel", struct{}{}, nil)
}

// Wait observes the stream and checks durable state when it ends or disconnects.
// +spec=`A disconnected observer reattaches the same Call; only persisted terminal state proves execution has ended.`
// Cancelling this observer does not cancel the Run.
func (c *Call) Wait(ctx context.Context) (contract.HarnessCall, error) {
	for attempt := 0; ; attempt++ {
		events, failures := c.Stream(ctx)
		for range events {
		}
		var observationErr error
		for err := range failures {
			observationErr = err
		}
		if err := ctx.Err(); err != nil {
			return contract.HarnessCall{}, model.WrapError(err)
		}
		value, err := c.Get(ctx)
		if err == nil && value.Phase.Terminal() {
			if value.Phase != contract.CallSucceeded {
				return value, &model.Error{Message: value.Error}
			}
			return value, nil
		}
		if err != nil {
			observationErr = err
		}
		if observationErr == nil {
			observationErr = model.ErrHarnessStreamIncomplete
		}
		if attempt == 2 || !model.IsRetryable(observationErr) {
			return value, observationErr
		}
		// Healthy streams do not poll SQL. Retry only after losing observation,
		// with at most three connections and a delay to avoid a tight loop.
		// Keep recovery limited to observed disconnect/unavailability cases;
		// extend it with regression cases from real failures, not speculative policies.
		c.client.Logger().WarnContext(ctx, "retry Harness observation", "call_id", c.id, "attempt", attempt+2)
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return value, model.WrapError(ctx.Err())
		case <-timer.C:
		}
	}
}
func (c *Call) Result(ctx context.Context) (*contract.HarnessResult, error) {
	value, err := c.Wait(ctx)
	return value.Result, err
}

// Stream observes one remote Call; reconnect and terminal-state decisions belong to Wait.
func (c *Call) Stream(ctx context.Context) (<-chan ui.Event, <-chan error) {
	events := make(chan ui.Event)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		response, err := c.client.Open(ctx, http.MethodGet, "/v1/harness/runs/"+url.PathEscape(c.id)+"/stream", nil)
		if err != nil {
			failures <- err
			return
		}
		defer response.Body.Close()
		if err := infra.DecodeResponseError(response); err != nil {
			failures <- err
			return
		}
		if err := infra.ReadEvents(ctx, response.Body, events); err != nil {
			failures <- err
		}
	}()
	return events, failures
}
