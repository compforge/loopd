package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"

	ui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
)

// Message is the writer returned by Speak. Repeating Speak with the same key
// restores the same message; one logical writer owns its event sequence.
type Message struct {
	mu      sync.Mutex
	client  *client
	value   loopd.Message
	next    uint64
	ended   bool
	pending json.RawMessage
}

type messageHandles struct {
	mu     sync.Mutex
	values map[string]*Message
}

func (handles *messageHandles) handle(c *client, value loopd.Message) *Message {
	handles.mu.Lock()
	message := handles.values[value.ID]
	if message == nil {
		message = &Message{client: c, value: value, next: 2}
		handles.values[value.ID] = message
	}
	handles.mu.Unlock()
	// A network write may hold this message's lock. Do not block unrelated
	// message handles while refreshing its snapshot.
	message.mu.Lock()
	defer message.mu.Unlock()
	// An ambiguous write must keep its original sequence until retried. A fresh
	// Speak snapshot cannot tell whether the caller has accounted for that write.
	if len(message.pending) > 0 {
		return message
	}
	if value.Revision >= message.value.Revision {
		message.value = value
	}
	if value.Revision >= message.next {
		message.next = value.Revision + 1
	}
	message.ended = message.ended || value.Ended()
	return message
}

func (message *Message) ID() string {
	message.mu.Lock()
	defer message.mu.Unlock()
	return message.value.ID
}

// Value observes the last locally known snapshot, not a server read.
func (message *Message) Value() loopd.Message {
	message.mu.Lock()
	defer message.mu.Unlock()
	value := message.value
	value.Content = append(json.RawMessage(nil), value.Content...)
	return value
}

// Emit updates this message (effect: write). The runtime assigns sequence numbers
// and retries transient failures; event.Seq is ignored. No transport ID is returned.
func (message *Message) Emit(ctx context.Context, event ui.Event) error {
	message.mu.Lock()
	defer message.mu.Unlock()
	if message.ended {
		return errors.New("message has ended")
	}
	if event.Op != ui.OpSet && event.Op != ui.OpAppend {
		return errors.New("Emit requires set or append; use End to finish sending")
	}
	return message.emit(ctx, event)
}

// End finishes only this message (effect: write), not the Conv or UI subscription.
// Repeating End, including after restoring the handle with Speak, is safe.
func (message *Message) End(ctx context.Context) error {
	message.mu.Lock()
	defer message.mu.Unlock()
	if message.ended {
		return nil
	}
	if err := message.emit(ctx, ui.End(0)); err != nil {
		return err
	}
	message.ended = true
	return nil
}

func (message *Message) emit(ctx context.Context, event ui.Event) error {
	event.Seq = message.next
	data, err := event.Marshal()
	if err != nil {
		return err
	}
	if len(message.pending) > 0 && !bytes.Equal(message.pending, data) {
		return errors.New("previous message update is unresolved; retry it before sending another update")
	}
	if err := message.client.write(ctx, "/v1/messages/"+url.PathEscape(message.value.ID)+"/events",
		struct {
			Event json.RawMessage `json:"event"`
		}{data}, nil); err != nil {
		message.pending = data
		return err
	}
	message.pending = nil
	if event.Seq >= message.next {
		message.next = event.Seq + 1
		var snapshot map[string]any
		if json.Unmarshal(message.value.Content, &snapshot) == nil {
			if next, err := ui.Apply(snapshot, event); err == nil {
				if event.Op == ui.OpEnd {
					meta, _ := next["meta"].(map[string]any)
					if meta == nil {
						meta = map[string]any{}
						next["meta"] = meta
					}
					meta["output"] = map[string]any{"ended": true}
				}
				message.value.Content, _ = ui.MarshalSnapshot(next)
			}
		}
		message.value.Revision = event.Seq
	}
	return nil
}
