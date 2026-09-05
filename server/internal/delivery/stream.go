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
// Only the main answer advances Last-Event-ID; other messages restart with their
// own full replay on reconnect. A side-message end never terminates the Task.
func (coordinator *Coordinator) Stream(ctx context.Context, taskID, conversationID, after string, deliver func(Event) error) error {
	response, err := coordinator.response(ctx, taskID)
	if err != nil {
		return err
	}
	if response.ConversationID != conversationID {
		return agentuerunner.ErrNotFound
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan messageDelivery)
	started := map[string]bool{}
	finished := map[string]bool{}
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
		rows, err := coordinator.repo.ListMessagesByTask(ctx, taskID, "", -1)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Purpose == "input" || row.ID == response.ID {
				continue
			}
			if row.Purpose == "output" && response.DeliveryState != "closed" {
				if err := start(row, ""); err != nil {
					return err
				}
			} else if err := snapshot(row); err != nil {
				return err
			}
		}
		return nil
	}
	if err := discover(); err != nil {
		return err
	}
	if response.DeliveryState == "closed" {
		if err := snapshot(response); err != nil {
			return err
		}
		end, err := agentueui.End(response.Revision + 1).Marshal()
		if err != nil {
			return err
		}
		return deliver(Event{MessageID: response.ID, Data: end})
	}
	// A lost bridge can only restart from the durable message snapshot.
	if _, err := coordinator.events.State(ctx, streamKey(response)); errors.Is(err, agentuerunner.ErrNotFound) {
		after = ""
	} else if err != nil {
		return err
	}
	if err := start(response, after); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var terminal *Event
	for {
		if terminal != nil {
			all := true
			for id := range started {
				if id != response.ID && !finished[id] {
					all = false
				}
			}
			if all {
				return deliver(*terminal)
			}
		}
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
				finished[value.message.ID] = true
				continue
			}
			parsed, err := agentueui.Parse(value.delivery.Data)
			if err != nil {
				return err
			}
			msg := visibleMessage(value.message)
			event := Event{MessageID: msg.ID, Message: &msg, Data: value.delivery.Data}
			if msg.ID == response.ID {
				event.ID = value.delivery.Cursor
				event.Persisted = event.ID != ""
				if parsed.Op == agentueui.OpEnd {
					if err := discover(); err != nil {
						return err
					}
					terminal = &event
					continue
				}
			}
			if err := deliver(event); err != nil {
				return err
			}
		}
	}
}
