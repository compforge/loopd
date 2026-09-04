// Package harness defines the provider boundary used by loop-server.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/compforge/loopd/api"
)

type Request struct {
	CallID         string
	IdempotencyKey string
	ExternalRef    string
	Cursor         string
	Target         string
	Prompt         string
	Tools          []api.Tool
}

type Observation struct {
	Phase       api.CallPhase
	ExternalRef string
	Cursor      string
	ActivityAt  time.Time
	Result      string
	Error       string
}

type Event struct {
	// Cursor is the provider's durable event identity, not loopd's event cursor.
	// Replayed events with the same non-empty cursor are projected only once.
	Cursor string
	Kind   string
	Data   json.RawMessage
}

const (
	// EventMessageDelta appends {"content":"..."} to the current Harness answer.
	EventMessageDelta = "message.delta"
)

type Emit func(context.Context, Event) error

// Adapter connects one addressable Harness to its provider. Ensure must return
// the same external execution for repeated requests with one IdempotencyKey.
// Ensure and Observe are bounded observations: a long Harness execution remains
// provider-owned while each call emits the events available within its context.
type Adapter interface {
	Ensure(context.Context, Request, Emit) (Observation, error)
	Observe(context.Context, Request, Emit) (Observation, error)
}

// OutcomeUnknownError means the provider may have accepted the action but its
// result cannot be proved. loop-server fails closed instead of starting again.
type OutcomeUnknownError struct {
	Err error
}

func (err *OutcomeUnknownError) Error() string {
	return fmt.Sprintf("Harness outcome is unknown: %v", err.Err)
}

func (err *OutcomeUnknownError) Unwrap() error { return err.Err }

func IsOutcomeUnknown(err error) bool {
	var target *OutcomeUnknownError
	return errors.As(err, &target)
}
