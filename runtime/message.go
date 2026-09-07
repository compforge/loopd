package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"sync"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
)

// messageStream is the writer returned by Tell. Repeating Tell with the same key
// restores the same message; one logical writer owns its event sequence.
type messageStream struct {
	Message
	mu      sync.Mutex
	client  *client
	value   contract.MessageInfo
	next    uint64
	ended   bool
	pending json.RawMessage
}

// Handles are caller-owned; Runtime never retains complete message snapshots.
func newMessageStream(c *client, value contract.MessageInfo) *messageStream {
	next := value.Revision + 1
	if next < 2 {
		next = 2
	}
	return &messageStream{Message: &remoteMessage{client: c, convID: value.ConversationID, id: value.ID}, client: c, value: value, next: next, ended: value.Status.Terminal()}
}

// Emit updates this message (effect: write). The runtime assigns sequence numbers
// and retries transient failures; event.Seq is ignored. No transport ID is returned.
func (message *messageStream) Emit(ctx context.Context, event ui.Event) error {
	message.mu.Lock()
	defer message.mu.Unlock()
	if message.ended {
		return errMessageEnded
	}
	if event.Op != ui.OpSet && event.Op != ui.OpAppend {
		return errInvalidEmit
	}
	return message.emit(ctx, event)
}

// End finishes only this message (effect: write), not the Conv or UI subscription.
// The optional terminal status defaults to completed. Repeating the same End,
// including after restoring the handle with Tell, is safe.
func (message *messageStream) End(ctx context.Context, statuses ...contract.MessageStatus) error {
	message.mu.Lock()
	defer message.mu.Unlock()
	status := contract.MessageStatusCompleted
	if len(statuses) > 1 {
		return errEndStatusCount
	}
	if len(statuses) == 1 {
		status = statuses[0]
	}
	if !status.Terminal() {
		return errEndStatusInvalid
	}
	if message.ended {
		if message.value.Status != status {
			return errEndStatusConflict
		}
		return nil
	}
	if err := message.emit(ctx, ui.End(0), status); err != nil {
		return err
	}
	message.ended = true
	return nil
}

func (message *messageStream) emit(ctx context.Context, event ui.Event, statuses ...contract.MessageStatus) error {
	event.Seq = message.next
	data, err := event.Marshal()
	if err != nil {
		return wrapError(err)
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
		return wrapError(err)
	}
	if len(message.pending) > 0 && !bytes.Equal(message.pending, data) {
		return errPendingMessageUpdate
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
		message.value.Revision = event.Seq
	}
	return nil
}
