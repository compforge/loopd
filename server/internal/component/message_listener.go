package component

import (
	"context"
	"errors"
	"log/slog"
	"time"

	runner "github.com/compforge/agentue/sdks/go/runner"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/server/internal/eventbridge"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

type MessageStreamRepository interface {
	GetMessage(context.Context, string) (model.Message, error)
	GetMessageState(context.Context, string) (repo.MessageState, error)
}

// MessageListener belongs to one Run stream request and observes its output.
// It never drives execution or creates Redis streams; MessageService owns writes.
type MessageListener struct {
	events runner.EventBridge
	repo   MessageStreamRepository
	id     string
	logger *slog.Logger
}

func NewMessageListener(events runner.EventBridge, repository MessageStreamRepository, id string, logger *slog.Logger) *MessageListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &MessageListener{events: events, repo: repository, id: id, logger: logger}
}

// Run follows Redis increments with low-frequency SQL revision checks.
// It loads full SQL bodies only initially and when repairing a missed delivery.
func (c *MessageListener) Run(ctx context.Context, deliver func(ui.Event) error) error {
	id := c.id
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	message, err := c.repo.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	start, err := ui.Start(message.Content, message.Revision)
	if err != nil {
		return err
	}
	if err := deliver(start); err != nil {
		return err
	}
	revision := message.Revision
	if visibleMessage(message).Ended() {
		return deliver(ui.End(revision))
	}
	incoming := make(chan ui.Event, 1)
	bridgeErr := make(chan error, 1)
	reading := false
	startReader := func() {
		if reading {
			return
		}
		reading = true
		go func() {
			err := (runner.Replayer{Bridge: c.events}).Stream(ctx, eventbridge.MessageKey(message.ID), "", func(d runner.Delivery) error {
				event, err := ui.Parse(d.Data)
				if err != nil {
					return err
				}
				select {
				case incoming <- event:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			bridgeErr <- err
		}()
	}
	startReader()
	repair := func() (bool, error) {
		state, err := c.repo.GetMessageState(ctx, id)
		if err != nil {
			return false, err
		}
		if state.Revision > revision {
			m, err := c.repo.GetMessage(ctx, id)
			if err != nil {
				return false, err
			}
			event, err := ui.Start(m.Content, m.Revision)
			if err != nil {
				return false, err
			}
			if err := deliver(event); err != nil {
				return false, err
			}
			revision = m.Revision
		}
		if state.Ended {
			return true, deliver(ui.End(revision))
		}
		return false, nil
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			if err := deliver(ui.Ping(revision)); err != nil {
				return err
			}
		case <-ticker.C:
			startReader()
			if done, err := repair(); done || err != nil {
				return err
			}
		case err := <-bridgeErr:
			reading = false
			// Repair from SQL during a Redis outage; the next tick reconnects the bridge.
			if err != nil && !errors.Is(err, context.Canceled) {
				c.logger.WarnContext(ctx, "Harness stream bridge unavailable", "message_id", id, "error", err)
			}
			if done, err := repair(); done || err != nil {
				return err
			}
		case event := <-incoming:
			if event.Op == ui.OpPing {
				if err := deliver(event); err != nil {
					return err
				}
				continue
			}
			if event.Seq <= revision {
				continue
			}
			if event.Op != ui.OpStart && event.Seq != revision+1 {
				if done, err := repair(); done || err != nil {
					return err
				}
				continue
			}
			if err := deliver(event); err != nil {
				return err
			}
			revision = event.Seq
			if event.Op == ui.OpEnd {
				return nil
			}
		}
	}
}
