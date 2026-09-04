package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	loopd "github.com/compforge/loopd"
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
	Tools        []loopd.Tool
}

func (service Harness) Prompt(ctx context.Context, prompt Prompt) (*Call, error) {
	var result loopd.HarnessCall
	err := service.client.do(ctx, http.MethodPost,
		"/v1/invocations/"+url.PathEscape(prompt.InvocationID)+"/harness-calls",
		promptRequest{
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
	value  loopd.HarnessCall
}

func (call *Call) Value() loopd.HarnessCall { return call.value }

func (call *Call) Refresh(ctx context.Context) (loopd.HarnessCall, error) {
	var result loopd.HarnessCall
	err := call.client.do(ctx, http.MethodGet, "/v1/harness-calls/"+url.PathEscape(call.value.ID), nil, &result)
	if err == nil {
		call.value = result
	}
	return result, err
}

func (call *Call) Wait(ctx context.Context) (loopd.HarnessCall, error) {
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
func (call *Call) Stream(ctx context.Context, after uint64) (<-chan loopd.HarnessEvent, <-chan error) {
	events := make(chan loopd.HarnessEvent)
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

func (call *Call) events(ctx context.Context, after uint64) ([]loopd.HarnessEvent, error) {
	var result page[loopd.HarnessEvent]
	path := "/v1/harness-calls/" + url.PathEscape(call.value.ID) + "/events?after=" + strconv.FormatUint(after, 10)
	err := call.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}
