package runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
)

var ErrCallConflict = errors.New("Harness call conflicts with an existing submission")

type Harness struct {
	client   *client
	registry registry
}
type Prompt = contract.HarnessRunRequest
type HarnessRegistration struct{ Key, DisplayName, Description string }

func newHarness(ctx context.Context, c *client, lease time.Duration, logger *slog.Logger) Harness {
	return Harness{client: c, registry: newRegistry(ctx, c, contract.ActorKindHarness, "harnesses", lease, logger)}
}
func (h Harness) Register(ctx context.Context, value HarnessRegistration) error {
	return h.registry.register(ctx, registration{key: value.Key, displayName: value.DisplayName, description: value.Description})
}

// Prompt submits a durable call. The Server owns driving and persisting output;
// returning or closing a Runtime never ends remote execution.
func (h Harness) Prompt(ctx context.Context, p Prompt) (*Call, error) {
	var value contract.HarnessCall
	if err := h.client.write(ctx, "/v1/harness/runs", p, &value); err != nil {
		if IsConflict(err) {
			return nil, errors.Join(ErrCallConflict, err)
		}
		return nil, err
	}
	return h.Call(value.ID), nil
}

// Call reconstructs a remote handle from a persisted run ID without starting work.
func (h Harness) Call(id string) *Call { return &Call{client: h.client, id: id} }

type Call struct {
	client *client
	id     string
}

func (c *Call) ID() string { return c.id }
func (c *Call) Get(ctx context.Context) (contract.HarnessCall, error) {
	var value contract.HarnessCall
	err := c.client.do(ctx, http.MethodGet, "/v1/harness/runs/"+url.PathEscape(c.id), nil, &value)
	return value, err
}
func (c *Call) Cancel(ctx context.Context) error {
	return c.client.write(ctx, "/v1/harness/runs/"+url.PathEscape(c.id)+"/cancel", struct{}{}, nil)
}

// Wait observes the Redis-backed stream, then reads the durable terminal result
// once. Cancelling this observer does not cancel the Run.
func (c *Call) Wait(ctx context.Context) (contract.HarnessCall, error) {
	events, failures := c.Stream(ctx)
	for range events {
	}
	for err := range failures {
		if err != nil {
			return contract.HarnessCall{}, err
		}
	}
	value, err := c.Get(ctx)
	if err != nil {
		return value, err
	}
	if !value.Phase.Terminal() {
		return value, errors.New("Harness stream ended before durable terminal state")
	}
	if value.Phase != contract.CallSucceeded {
		return value, errors.New(value.Error)
	}
	return value, nil
}
func (c *Call) Result(ctx context.Context) (*contract.HarnessResult, error) {
	value, err := c.Wait(ctx)
	return value.Result, err
}

// Stream reads the Server's Redis-backed SSE delivery. Reconnect yields a fresh
// snapshot, so no process-local event array or per-token SQL polling is needed.
func (c *Call) Stream(ctx context.Context) (<-chan ui.Event, <-chan error) {
	events := make(chan ui.Event)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		response, err := c.client.open(ctx, http.MethodGet, "/v1/harness/runs/"+url.PathEscape(c.id)+"/stream", nil)
		if err != nil {
			failures <- err
			return
		}
		defer response.Body.Close()
		if err := decodeResponseError(response); err != nil {
			failures <- err
			return
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 32<<20)
		var data strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
				continue
			}
			if line != "" || data.Len() == 0 {
				continue
			}
			event, err := ui.Parse([]byte(data.String()))
			data.Reset()
			if err != nil {
				failures <- err
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
			if event.Op == ui.OpEnd {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			failures <- err
		} else {
			failures <- io.ErrUnexpectedEOF
		}
	}()
	return events, failures
}
