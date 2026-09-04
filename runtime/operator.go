package runtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/compforge/loopd/api"
)

type Operator struct {
	client *client
}

func (operator Operator) Events(ctx context.Context, operatorID string, after uint64) ([]api.OperatorEvent, error) {
	var result api.Page[api.OperatorEvent]
	path := "/v1/operators/" + url.PathEscape(operatorID) + "/events?after=" + strconv.FormatUint(after, 10)
	err := operator.client.do(ctx, http.MethodGet, path, nil, &result)
	return result.Data, err
}

// Watch observes durable changes that can advance an Operator. Token deltas and
// Activity projections are deliberately excluded so they cannot create a
// Reconcile storm. A controller bridge can create a CRD for invocation.created
// and enqueue an existing CRD when an Interaction or Harness Call changes.
func (operator Operator) Watch(ctx context.Context, operatorID string, after uint64) (<-chan api.OperatorEvent, <-chan error) {
	events := make(chan api.OperatorEvent)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		cursor := after
		ticker := time.NewTicker(operator.client.pollInterval)
		defer ticker.Stop()
		for {
			page, err := operator.Events(ctx, operatorID, cursor)
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
					cursor = event.Event.Cursor
				}
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

func (operator Operator) Pending(ctx context.Context, operatorID string) ([]api.Invocation, error) {
	var result api.Page[api.Invocation]
	err := operator.client.do(ctx, http.MethodGet, "/v1/operators/"+url.PathEscape(operatorID)+"/invocations", nil, &result)
	return result.Data, err
}

func (operator Operator) Accept(ctx context.Context, invocationID string, resource api.ResourceRef) (api.Invocation, error) {
	var result api.Invocation
	err := operator.client.do(ctx, http.MethodPost, "/v1/invocations/"+url.PathEscape(invocationID)+"/accept",
		api.AcceptInvocationRequest{Resource: resource}, &result)
	return result, err
}
