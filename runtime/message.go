package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
)

// Message is the writer returned by Speak. Repeating Speak with the same key
// restores the same message; one logical writer owns its event sequence.
type Message struct {
	mu      sync.Mutex
	client  *client
	value   contract.Message
	next    uint64
	ended   bool
	pending json.RawMessage
}

// Handles are caller-owned; Runtime never retains complete message snapshots.
type messageHandles struct{}

func (*messageHandles) handle(c *client, value contract.Message) *Message {
	next := value.Revision + 1
	if next < 2 {
		next = 2
	}
	return &Message{client: c, value: value, next: next, ended: value.Ended()}
}

func (message *Message) ID() string {
	message.mu.Lock()
	defer message.mu.Unlock()
	return message.value.ID
}

// Value observes the last locally known snapshot, not a server read.
func (message *Message) Value() contract.Message {
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
// The optional terminal status defaults to completed. Repeating the same End,
// including after restoring the handle with Speak, is safe.
func (message *Message) End(ctx context.Context, statuses ...contract.MessageStatus) error {
	message.mu.Lock()
	defer message.mu.Unlock()
	status := contract.MessageStatusCompleted
	if len(statuses) > 1 {
		return errors.New("End accepts at most one status")
	}
	if len(statuses) == 1 {
		status = statuses[0]
	}
	if !status.Terminal() {
		return errors.New("End requires a terminal message status")
	}
	if message.ended {
		if message.value.Status != status {
			return errors.New("message already ended with a different status")
		}
		return nil
	}
	if err := message.emit(ctx, ui.End(0), status); err != nil {
		return err
	}
	message.ended = true
	return nil
}

func (message *Message) emit(ctx context.Context, event ui.Event, statuses ...contract.MessageStatus) error {
	event.Seq = message.next
	data, err := event.Marshal()
	if err != nil {
		return err
	}
	request := struct {
		Event  json.RawMessage        `json:"event"`
		Status contract.MessageStatus `json:"status,omitempty"`
	}{Event: data}
	if len(statuses) != 0 {
		request.Status = statuses[0]
	}
	data, err = json.Marshal(request)
	if err != nil {
		return err
	}
	if len(message.pending) > 0 && !bytes.Equal(message.pending, data) {
		return errors.New("previous message update is unresolved; retry it before sending another update")
	}
	if err := message.client.write(ctx, "/v1/messages/"+url.PathEscape(message.value.ID)+"/events", json.RawMessage(data), nil); err != nil {
		message.pending = data
		return err
	}
	message.pending = nil
	if event.Seq >= message.next {
		message.next = event.Seq + 1
		if event.Op == ui.OpEnd {
			message.value.Status = request.Status
		}
		var snapshot map[string]any
		if json.Unmarshal(message.value.Content, &snapshot) == nil {
			if next, err := ui.Apply(snapshot, event); err == nil {
				message.value.Content, _ = ui.MarshalSnapshot(next)
			}
		}
		message.value.Revision = event.Seq
	}
	return nil
}
