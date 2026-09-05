package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	agentueui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	provider "github.com/compforge/loopd/harness"
	"github.com/qiankunli/go-stdx/uuid"
)

var ErrCallConflict = errors.New("harness call conflicts with an existing effect")

type Harness struct {
	conv     Conv
	registry registry
	state    *harnessState
}

type harnessState struct {
	ctx      context.Context
	adapters map[string]provider.Adapter
	logger   *slog.Logger
	mu       sync.Mutex
	calls    map[string]*Call
}

type Prompt struct {
	// ConversationID directs visible output independently of a user Delivery.
	ConversationID string
	// IdempotencyKey belongs to the caller's business scope, not the UI stream.
	IdempotencyKey string
	EffectKey      string
	Target         string
	Text           string
	Tools          []loopd.Tool
	Actor          *loopd.ActorRef
	Timeout        time.Duration
}

type HarnessRegistration struct {
	Key         string
	DisplayName string
	Description string
}

func newHarness(
	ctx context.Context,
	client *client,
	leaseDuration time.Duration,
	adapters map[string]provider.Adapter,
	logger *slog.Logger,
) Harness {
	values := make(map[string]provider.Adapter, len(adapters))
	for key, adapter := range adapters {
		values[key] = adapter
	}
	return Harness{registry: newRegistry(ctx, client, "harness", "harnesses", leaseDuration, logger), state: &harnessState{
		ctx: ctx, adapters: values, logger: logger,
		calls: make(map[string]*Call),
	}}
}

func (service Harness) Register(ctx context.Context, value HarnessRegistration) error {
	return service.registry.register(ctx, registration{
		key: value.Key, displayName: value.DisplayName, description: value.Description,
	})
}

// Prompt is a Verb (effect: write) starting or resuming one process-local Call. The effect identity avoids
// duplicate starts while this Runtime is alive. A production Adapter such as
// agentd must additionally make IdempotencyKey durable across process restarts.
func (service Harness) Prompt(ctx context.Context, prompt Prompt) (*Call, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (prompt.ConversationID == "" || prompt.IdempotencyKey == "") || prompt.EffectKey == "" || prompt.Target == "" || prompt.Text == "" {
		return nil, errors.New("conversation and idempotency key, effect key, Harness target, and prompt are required")
	}
	if prompt.Timeout < 0 {
		return nil, errors.New("timeout cannot be negative")
	}
	if prompt.Actor != nil {
		if !prompt.Actor.ValidTarget() {
			return nil, errors.New("invalid output actor")
		}
		author := *prompt.Actor
		prompt.Actor = &author
	}
	adapter := service.state.adapters[prompt.Target]
	if adapter == nil {
		return nil, fmt.Errorf("harness target %q is not configured", prompt.Target)
	}
	fingerprint, err := promptFingerprint(prompt)
	if err != nil {
		return nil, err
	}
	effectID := prompt.IdempotencyKey

	service.state.mu.Lock()
	if existing := service.state.calls[effectID]; existing != nil {
		service.state.mu.Unlock()
		if existing.fingerprint != fingerprint {
			return nil, fmt.Errorf("%w: conversation %q effect %q", ErrCallConflict, prompt.ConversationID, prompt.EffectKey)
		}
		return existing, nil
	}
	callID := uuid.V7()
	request := provider.Request{
		CallID:         callID,
		IdempotencyKey: effectID,
		Timeout:        prompt.Timeout, Prompt: prompt.Text, Tools: append([]loopd.Tool(nil), prompt.Tools...),
	}
	providerCall, err := adapter.Prompt(service.state.ctx, request)
	if err != nil {
		service.state.mu.Unlock()
		return nil, fmt.Errorf("start harness %q: %w", prompt.Target, err)
	}
	if providerCall == nil || providerCall.ID() == "" {
		service.state.mu.Unlock()
		return nil, fmt.Errorf("start harness %q: adapter returned a call without an ID", prompt.Target)
	}
	now := time.Now().UTC()
	call := &Call{
		value: loopd.HarnessCall{
			ID: providerCall.ID(), EffectKey: prompt.EffectKey,
			Target: prompt.Target, Phase: loopd.CallRunning,
			Timestamped: loopd.Timestamped{CreatedAt: now, UpdatedAt: now},
		},
		fingerprint: fingerprint,
		changed:     make(chan struct{}),
		done:        make(chan struct{}),
	}
	service.state.calls[effectID] = call
	service.state.mu.Unlock()
	service.state.logger.InfoContext(ctx, "harness call started",
		"conversation_id", prompt.ConversationID,
		"effect_key", prompt.EffectKey,
		"target", prompt.Target,
		"call_id", providerCall.ID(),
	)

	go call.follow(service.state.ctx, providerCall, service.conv, prompt, service.state.logger)
	return call, nil
}

func promptFingerprint(prompt Prompt) ([32]byte, error) {
	encoded, err := json.Marshal(prompt)
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode Harness prompt: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

type Call struct {
	mu          sync.Mutex
	value       loopd.HarnessCall
	fingerprint [32]byte
	events      []agentueui.Event
	changed     chan struct{}
	done        chan struct{}
	terminalErr error
}

// Value is a Verb (effect: read) observing the locally known execution state.
func (call *Call) Value() loopd.HarnessCall {
	call.mu.Lock()
	defer call.mu.Unlock()
	return call.value
}

// Wait is a Verb (effect: read); cancelling the wait does not cancel execution.
func (call *Call) Wait(ctx context.Context) (loopd.HarnessCall, error) {
	select {
	case <-ctx.Done():
		return call.Value(), ctx.Err()
	case <-call.done:
		call.mu.Lock()
		defer call.mu.Unlock()
		return call.value, call.terminalErr
	}
}

// Stream observes this Call from its locally retained beginning, then follows it.
// It is not a page subscription; no Redis/SSE replay cursor is required.
func (call *Call) Stream(ctx context.Context) (<-chan agentueui.Event, <-chan error) {
	events := make(chan agentueui.Event)
	errors := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errors)
		index := 0
		for {
			call.mu.Lock()
			pending := append([]agentueui.Event(nil), call.events[index:]...)
			index = len(call.events)
			changed := call.changed
			terminal := call.value.Phase.Terminal()
			call.mu.Unlock()
			for _, event := range pending {
				select {
				case <-ctx.Done():
					errors <- ctx.Err()
					return
				case events <- event:
				}
			}
			if terminal {
				return
			}
			select {
			case <-ctx.Done():
				errors <- ctx.Err()
				return
			case <-changed:
			}
		}
	}()
	return events, errors
}

func (call *Call) follow(
	ctx context.Context,
	providerCall provider.Call,
	conv Conv,
	prompt Prompt,
	logger *slog.Logger,
) {
	var publishErr error
	var output *Message
	for event := range providerCall.Events() {
		if publishErr != nil {
			continue
		}
		value, err := agentueui.Parse(event.Data)
		if err != nil {
			publishErr = fmt.Errorf("decode harness AgentUE event: %w", err)
			continue
		}
		if value.Op != agentueui.OpSet && value.Op != agentueui.OpAppend {
			publishErr = fmt.Errorf("harness may only publish AgentUE set or append events, got %q", value.Op)
			continue
		}
		// Stamp once before delivery: retries must carry the same event bytes.
		// AgentUE's timestamp is the visible output time, not Task completion.
		if value.Timestamp == nil {
			now := time.Now().UnixMilli()
			value.Timestamp = &now
		}
		if value.Block != nil {
			// AgentUE retains extra block fields on creation/full replacement;
			// masked deltas only update their field, so identity never appends.
			value.Block["call_id"] = call.value.ID
			value.Block["effect_key"] = call.value.EffectKey
		}
		if output == nil {
			var message *Message
			var err error
			content, _ := json.Marshal(map[string]any{"version": "1.0", "biz": "chat", "meta": map[string]any{"effect_key": prompt.EffectKey}, "blocks": []any{}})
			author := loopd.ActorRef{Kind: loopd.ActorKindHarness, Key: call.value.ID}
			if prompt.Actor != nil {
				author = *prompt.Actor
			}
			message, err = conv.Speak(ctx, prompt.ConversationID, loopd.SpeakRequest{
				Stream: true, Key: prompt.IdempotencyKey, Actor: author, Content: content,
			})
			if err != nil {
				publishErr = err
				continue
			}
			output = message
		}
		err = output.Emit(ctx, value)
		if err != nil {
			publishErr = err
			continue
		}
		call.appendEvent(value)
	}
	result, waitErr := providerCall.Wait(ctx)
	if output != nil && publishErr == nil && ctx.Err() == nil {
		publishErr = output.End(ctx)
	}
	err := errors.Join(publishErr, waitErr)
	phase := loopd.CallSucceeded
	if err != nil {
		phase = loopd.CallFailed
	}
	call.finish(phase, result.Text, err)
	value := call.Value()
	logContext := context.WithoutCancel(ctx)
	if err != nil {
		logger.ErrorContext(logContext, "harness call failed",
			"conversation_id", prompt.ConversationID,
			"effect_key", value.EffectKey,
			"target", value.Target,
			"call_id", providerCall.ID(),
			"error", err,
		)
		return
	}
	logger.InfoContext(logContext, "harness call completed",
		"conversation_id", prompt.ConversationID,
		"effect_key", value.EffectKey,
		"target", value.Target,
		"call_id", providerCall.ID(),
	)
}

func (call *Call) appendEvent(event agentueui.Event) {
	call.mu.Lock()
	call.events = append(call.events, event)
	now := time.Now().UTC()
	call.value.LastActivityAt = &now
	call.value.UpdatedAt = now
	close(call.changed)
	call.changed = make(chan struct{})
	call.mu.Unlock()
}

func (call *Call) finish(phase loopd.CallPhase, result string, err error) {
	call.mu.Lock()
	call.value.Phase = phase
	call.value.Result = result
	if err != nil {
		call.value.Error = err.Error()
		call.terminalErr = err
	}
	call.value.UpdatedAt = time.Now().UTC()
	close(call.changed)
	close(call.done)
	call.mu.Unlock()
}
