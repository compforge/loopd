// Package harness defines the intelligent execution boundary driven by loop-server.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

type Request struct {
	EffectKey      string
	ScopeKey       string
	ExecutionRef   string
	Timeout        time.Duration
	CallID         string
	IdempotencyKey string
	Prompt         string
	Tools          []contract.Tool
}

type Event struct {
	// Data is an AgentUE set or append event. The Adapter describes visible
	// Harness progress; Server assigns Message revisions and persists the content.
	Data json.RawMessage
}

// Result carries the Adapter's interpretation of native final output.
type Result struct {
	Text string
	JSON json.RawMessage
}

func (r Result) Value() contract.HarnessResult {
	if r.JSON != nil {
		return contract.HarnessResult{Format: "json", Content: r.JSON}
	}
	return contract.TextResult(r.Text)
}

var ErrRecoveryUnsupported = errors.New("harness cannot recover this execution")

// ObservationError means execution outcome is unknown; a recoverable Adapter may reattach.
type ObservationError struct{ Err error }

func (e *ObservationError) Error() string { return e.Err.Error() }
func (e *ObservationError) Unwrap() error { return e.Err }

// Recoverable reattaches the same execution and replays the same ordered AgentUE
// events from its beginning. It must never silently start a second execution.
type Recoverable interface {
	Resume(context.Context, Request) (Call, error)
}

// ExecutionReference exposes the native locator as opaque persisted data.
type ExecutionReference interface{ ExecutionRef() string }

// Call is a Harness-owned execution handle. Events closes after the execution
// reaches a terminal state; Wait returns the same terminal result.
type Call interface {
	ID() string
	Events() <-chan Event
	Wait(context.Context) (Result, error)
}

// Adapter connects one addressable Harness to loop-server. Prompt returns
// promptly with a Call handle; the Harness remains the execution owner.
type Adapter interface {
	Prompt(context.Context, Request) (Call, error)
}
