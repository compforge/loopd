package delivery

import (
	"context"
	"errors"
	"time"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/model"
)

type messageDelivery struct {
	message  model.Message
	delivery agentuerunner.Delivery
	done     bool
	err      error
}

// Stream multiplexes messages without folding their models together.
// The UI transport advances Last-Event-ID; each message replays its independent
// stream on reconnect. A message's end never terminates the Chat transport.
func (coordinator *Coordinator) Stream(ctx context.Context, taskID, conversationID, after string, deliver func(Event) error) error {
	input, err := coordinator.input(ctx, taskID)
	if err != nil {
		return err
	}
	if input.ConversationID != conversationID {
		return agentuerunner.ErrNotFound
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan messageDelivery)
	started := map[string]bool{}
	revisions := map[string]uint64{}
	start := func(message model.Message, cursor string) error {
		if started[message.ID] {
			return nil
		}
		if err := coordinator.ensureStream(ctx, message); err != nil {
			return err
		}
		started[message.ID] = true
		go func() {
			send := func(value messageDelivery) error {
				select {
				case incoming <- value:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			err := (agentuerunner.Replayer{Bridge: coordinator.events}).Stream(ctx, streamKey(message), cursor, func(value agentuerunner.Delivery) error {
				return send(messageDelivery{message: message, delivery: value})
			})
			_ = send(messageDelivery{message: message, done: true, err: err})
		}()
		return nil
	}
	snapshot := func(message model.Message) error {
		revision := message.Revision
		if revision == 0 {
			revision = 1
		}
		if revisions[message.ID] >= revision {
			return nil
		}
		event, err := agentueui.Start(message.Content, revision)
		if err != nil {
			return err
		}
		data, err := event.Marshal()
		if err != nil {
			return err
		}
		msg := visibleMessage(message)
		if err := deliver(Event{MessageID: message.ID, Message: &msg, Data: data}); err != nil {
			return err
		}
		revisions[message.ID] = revision
		return nil
	}
	discover := func() error {
		rows, err := coordinator.repo.ListDeliveryMessages(ctx, conversationID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			// SQL snapshots repair missed bridge deliveries without asking writers
			// or Operators to know whether a browser was connected.
			if err := snapshot(row); err != nil {
				return err
			}
			if row.Purpose == "output" && !visibleMessage(row).Ended() {
				if err := start(row, ""); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := discover(); err != nil {
		return err
	}
	control := transportMessage(input)
	// A lost bridge can only restart from the durable message snapshot.
	if _, err := coordinator.events.State(ctx, streamKey(control)); errors.Is(err, agentuerunner.ErrNotFound) {
		after = ""
	} else if err != nil {
		return err
	}
	if err := start(control, after); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := discover(); err != nil {
				return err
			}
		case value := <-incoming:
			if value.done {
				if value.err != nil {
					return value.err
				}
				continue
			}
			msg := visibleMessage(value.message)
			event := Event{MessageID: msg.ID, Message: &msg, Data: value.delivery.Data}
			if msg.ID == control.ID {
				event.Message = nil
				event.ID = value.delivery.Cursor
				event.Persisted = event.ID != ""
			}
			if err := deliver(event); err != nil {
				return err
			}
		}
	}
}
