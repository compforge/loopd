// Package harness defines the intelligent execution boundary used by loop-runtime.
package harness

import (
	"context"
	"encoding/json"

	loopd "github.com/compforge/loopd"
)

type Request struct {
	CallID         string
	IdempotencyKey string
	Prompt         string
	Tools          []loopd.Tool
}

type Event struct {
	// Data is an AgentUE set or append event. The Adapter describes visible
	// Harness progress; loop-runtime assigns seq and publishes it to loop-server.
	Data json.RawMessage
}

type Result struct {
	Text string
}

// Call is a Harness-owned execution handle. Events closes after the execution
// reaches a terminal state; Wait returns the same terminal result.
type Call interface {
	ID() string
	Events() <-chan Event
	Wait(context.Context) (Result, error)
}

// Adapter connects one addressable Harness to loop-runtime. Prompt returns
// promptly with a Call handle; the Harness remains the execution owner.
type Adapter interface {
	Prompt(context.Context, Request) (Call, error)
}
