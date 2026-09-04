package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/compforge/loopd/api"
)

type Harness struct {
	client *client
}

type Prompt struct {
	InvocationID string
	OwnerUID     string
	EffectKey    string
	Target       string
	Text         string
	Tools        []api.Tool
}

func (service Harness) Prompt(ctx context.Context, prompt Prompt) (*Call, error) {
	var result api.HarnessCall
	err := service.client.do(ctx, http.MethodPost,
		"/v1/invocations/"+url.PathEscape(prompt.InvocationID)+"/harness-calls",
		api.PromptRequest{
			OwnerUID: prompt.OwnerUID, EffectKey: prompt.EffectKey,
			Target: prompt.Target, Prompt: prompt.Text, Tools: prompt.Tools,
		}, &result)
	if err != nil {
		return nil, err
	}
	return &Call{client: service.client, value: result}, nil
}

type Call struct {
	client *client
	value  api.HarnessCall
}

func (call *Call) Value() api.HarnessCall { return call.value }

func (call *Call) Refresh(ctx context.Context) (api.HarnessCall, error) {
	var result api.HarnessCall
	err := call.client.do(ctx, http.MethodGet, "/v1/harness-calls/"+url.PathEscape(call.value.ID), nil, &result)
	if err == nil {
		call.value = result
	}
	return result, err
}

func (call *Call) Wait(ctx context.Context) (api.HarnessCall, error) {
	if call.value.Phase.Terminal() {
		return call.value, nil
	}
	ticker := time.NewTicker(call.client.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return call.value, ctx.Err()
		case <-ticker.C:
			value, err := call.Refresh(ctx)
			if err != nil {
				return call.value, err
			}
			if value.Phase.Terminal() {
				return value, nil
			}
		}
	}
}

// Stream polls persisted Harness Events. The stream can be recreated with the
// last cursor after process or network interruption; it does not own the Call.
func (call *Call) Stream(ctx context.Context, after uint64) (<-chan api.HarnessEvent, <-chan error) {
	events := make(chan api.HarnessEvent)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		cursor := after
		ticker := time.NewTicker(call.client.pollInterval)
		defer ticker.Stop()
		for {
			page, err := call.events(ctx, cursor)
			if err != nil {
				errors <- err
				return
			}
			for _, event := range page {
				select {
				case <-ctx.Done():
					errors <- ctx.Err()
					return
				case events <- event:
					cursor = event.Cursor
				}
			}
			value, err := call.Refresh(ctx)
			if err != nil {
				errors <- err
				return
			}
			if value.Phase.Terminal() && len(page) == 0 {
				return
			}
			select {
			case <-ctx.Done():
				errors <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()
	return events, errors
}

func (call *Call) events(ctx context.Context, after uint64) ([]api.HarnessEvent, error) {
	var result api.Page[api.HarnessEvent]
	path := "/v1/harness-calls/" + url.PathEscape(call.value.ID) + "/events?after=" + strconv.FormatUint(after, 10)
	err := call.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}
