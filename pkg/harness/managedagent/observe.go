package managedagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/compforge/loopd/pkg/harness"
	"github.com/compforge/loopd/pkg/harness/internal/httpclient"
)

func (adapter *Adapter) observe(ctx context.Context, call *call) (result harness.Result, err error) {
	parent := ctx
	defer func() {
		var stop *StopError
		if err != nil && !errors.As(err, &stop) && parent.Err() == nil {
			err = &harness.ObservationError{Err: err}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, adapter.config.StreamTimeout)
	defer cancel()
	state := &projection{sessionID: call.sessionID, seen: make(map[string]bool)}
	params := anthropic.BetaSessionEventStreamParams{}
	if adapter.config.PreviewDeltas {
		params.EventDeltas = []anthropic.BetaManagedAgentsDeltaType{"agent.message"}
	}
	// Subscribe before listing: execution may have already produced output while
	// Session creation was returning. History fills that gap; IDs deduplicate the
	// overlap. This is initial catch-up, not a durable recovery mechanism.
	stream := adapter.client.Beta.Sessions.Events.StreamEvents(ctx, call.sessionID, params,
		option.WithRequestTimeout(adapter.config.StreamTimeout),
		option.WithHTTPClient(httpclient.DoFunc(adapter.http.DoStream)))
	defer stream.Close()
	if err = stream.Err(); err != nil {
		return harness.Result{}, fmt.Errorf("open managedagent stream: %w", err)
	}
	page := adapter.client.Beta.Sessions.Events.ListAutoPaging(ctx, call.sessionID,
		anthropic.BetaSessionEventListParams{Limit: anthropic.Int(100)})
	for page.Next() {
		var event anthropic.BetaManagedAgentsStreamSessionEventsUnion
		if err := json.Unmarshal([]byte(page.Current().RawJSON()), &event); err != nil {
			return harness.Result{}, fmt.Errorf("decode managedagent history: %w", err)
		}
		if done, err := consume(ctx, call, state, event); done || err != nil {
			return harness.Result{Text: state.text}, err
		}
	}
	if err := page.Err(); err != nil {
		return harness.Result{Text: state.text}, fmt.Errorf("read managedagent history: %w", err)
	}
	for stream.Next() {
		if done, err := consume(ctx, call, state, stream.Current()); done || err != nil {
			return harness.Result{Text: state.text}, err
		}
	}
	err = stream.Err()
	if err == nil {
		// An SSE disconnect is not evidence of remote execution completion.
		err = io.ErrUnexpectedEOF
	}
	return harness.Result{Text: state.text}, fmt.Errorf("read managedagent stream: %w", err)
}

func consume(ctx context.Context, call *call, state *projection, event anthropic.BetaManagedAgentsStreamSessionEventsUnion) (bool, error) {
	updates, done, err := state.consume(event)
	for _, update := range updates {
		data, marshalErr := update.Marshal()
		if marshalErr != nil {
			return true, fmt.Errorf("encode managedagent projection: %w", marshalErr)
		}
		select {
		case call.events <- harness.Event{Data: data}:
		case <-ctx.Done():
			return true, ctx.Err()
		}
	}
	return done, err
}
