// Package managedagent connects Harness calls to the Claude Managed Agents API
// through the official Go SDK. agentd is one compatible backend, not a dependency.
package managedagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/compforge/loopd/pkg/harness"
)

type Config struct {
	IdempotentSubmission bool // Backend guarantees durable Idempotency-Key on Session creation.
	APIKey               string
	BaseURL              string // Empty uses the SDK's official endpoint.
	AgentID              string
	EnvironmentID        string
	// ConfigureSession optionally selects versions, toolsets or other SDK session
	// options per call. It must resolve Request.Tools when that list is non-empty;
	// tool descriptors alone do not provide remote tool implementations.
	// InitialEvents is owned by Prompt and set after this callback.
	ConfigureSession func(context.Context, harness.Request, *anthropic.BetaSessionNewParams) error
	HTTPTimeout      time.Duration
	StreamTimeout    time.Duration
	MaxConnections   int
	// PreviewDeltas requests best-effort token previews. Leave false for backends
	// that only support durable events; both paths publish AgentUE updates.
	PreviewDeltas bool
	Logger        *slog.Logger
}

type Adapter struct {
	config Config
	client anthropic.Client
}

func New(config Config) (*Adapter, error) {
	if config.APIKey == "" || config.AgentID == "" || config.EnvironmentID == "" {
		return nil, errors.New("managedagent API key, agent ID and environment ID are required")
	}
	if config.HTTPTimeout < 0 || config.StreamTimeout < 0 || config.MaxConnections < 0 {
		return nil, errors.New("managedagent timeouts and connection limit must not be negative")
	}
	if config.HTTPTimeout == 0 {
		config.HTTPTimeout = 30 * time.Second
	}
	if config.StreamTimeout == 0 {
		config.StreamTimeout = 30 * time.Minute
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 32
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: config.HTTPTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = config.HTTPTimeout
	transport.ResponseHeaderTimeout = config.HTTPTimeout
	transport.MaxConnsPerHost = config.MaxConnections
	transport.MaxIdleConns = config.MaxConnections
	transport.MaxIdleConnsPerHost = config.MaxConnections
	transport.IdleConnTimeout = time.Minute
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(), option.WithAPIKey(config.APIKey),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithRequestTimeout(config.HTTPTimeout),
		// A generic compatible backend need not deduplicate Session creation.
		// Do not automatically repeat a write whose outcome is unknown.
		option.WithMaxRetries(0),
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	return &Adapter{config: config, client: anthropic.NewClient(opts...)}, nil
}

// Prompt creates one remote Session and returns a handle without waiting for the
// model. The parent context bounds observation, not the remote Session lifetime.
// Request.Timeout bounds submission and observation; cancelling Wait only stops
// that waiter. Runtime consumes Events concurrently with Wait.
//
// +spec=`Remote execution belongs to the backend; recovery reattaches a stored Session or repeats an explicitly idempotent submission.`
// +why=`Idempotency-Key support is backend-specific, not a guarantee of the SDK protocol.`
func (adapter *Adapter) Prompt(ctx context.Context, request harness.Request) (harness.Call, error) {
	if request.CallID == "" || request.IdempotencyKey == "" || request.Prompt == "" {
		return nil, errors.New("call ID, idempotency key, and prompt are required")
	}
	if request.Timeout < 0 {
		return nil, errors.New("managedagent call timeout must not be negative")
	}
	if len(request.Tools) > 0 && adapter.config.ConfigureSession == nil {
		return nil, errors.New("managedagent request tools require ConfigureSession to resolve remote toolsets")
	}
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	params := anthropic.BetaSessionNewParams{
		Agent:         anthropic.BetaSessionNewParamsAgentUnion{OfString: anthropic.String(adapter.config.AgentID)},
		EnvironmentID: adapter.config.EnvironmentID,
	}
	if adapter.config.ConfigureSession != nil {
		if err := adapter.config.ConfigureSession(ctx, request, &params); err != nil {
			cancel()
			return nil, fmt.Errorf("configure managedagent session: %w", err)
		}
	}
	params.InitialEvents = []anthropic.BetaSessionNewParamsInitialEventUnion{{
		OfUserMessage: &anthropic.BetaManagedAgentsUserMessageEventParams{
			Type: anthropic.BetaManagedAgentsUserMessageEventParamsTypeUserMessage,
			Content: []anthropic.BetaManagedAgentsUserMessageEventParamsContentUnion{{
				OfText: &anthropic.BetaManagedAgentsTextBlockParam{Type: anthropic.BetaManagedAgentsTextBlockTypeText, Text: request.Prompt},
			}},
		},
	}}
	var session anthropic.BetaManagedAgentsSession
	var err error
	if request.ExecutionRef != "" {
		session.ID = request.ExecutionRef
	} else {
		opts := []option.RequestOption{}
		if adapter.config.IdempotentSubmission {
			opts = append(opts, option.WithHeader("Idempotency-Key", request.IdempotencyKey))
		}
		created, e := adapter.client.Beta.Sessions.New(ctx, params, opts...)
		err = e
		if created != nil {
			session = *created
		}
	}
	if err != nil {
		cancel()
		return nil, &harness.ObservationError{Err: fmt.Errorf("create managedagent session: %w", err)}
	}
	call := &call{id: request.CallID, sessionID: session.ID, events: make(chan harness.Event, 128), done: make(chan struct{})}
	adapter.config.Logger.InfoContext(ctx, "managedagent call submitted", "call_id", call.id, "session_id", session.ID)
	go func() {
		defer cancel()
		defer close(call.done)
		defer close(call.events)
		call.result, call.err = adapter.observe(ctx, call)
		if call.err != nil {
			// The caller receives the detailed SDK error. Do not log its response
			// body here: compatible servers may echo user input or credentials.
			adapter.config.Logger.WarnContext(ctx, "managedagent observation stopped", "call_id", call.id, "session_id", session.ID)
		} else {
			adapter.config.Logger.InfoContext(ctx, "managedagent call completed", "call_id", call.id, "session_id", session.ID)
		}
	}()
	return call, nil
}

type call struct {
	id        string
	sessionID string
	events    chan harness.Event
	done      chan struct{}
	result    harness.Result
	err       error
}

func (call *call) ID() string                   { return call.id }
func (call *call) Events() <-chan harness.Event { return call.events }

func (call *call) Wait(ctx context.Context) (harness.Result, error) {
	select {
	case <-ctx.Done():
		return harness.Result{}, ctx.Err()
	case <-call.done:
		return call.result, call.err
	}
}

var _ harness.Adapter = (*Adapter)(nil)

func (c *call) ExecutionRef() string { return c.sessionID }
func (a *Adapter) Resume(ctx context.Context, r harness.Request) (harness.Call, error) {
	if a.config.PreviewDeltas {
		return nil, fmt.Errorf("%w: preview deltas cannot be stably replayed", harness.ErrRecoveryUnsupported)
	}
	if r.ExecutionRef == "" && !a.config.IdempotentSubmission {
		return nil, harness.ErrRecoveryUnsupported
	}
	return a.Prompt(ctx, r)
}
