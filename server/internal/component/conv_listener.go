package component

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	agentuerunner "github.com/compforge/agentue/sdks/go/runner"
	agentueui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
)

const discoveryPageSize = 100

type Event struct {
	MessageID string
	Message   *contract.Message
	Data      json.RawMessage
}

type ConvMessageRepository interface {
	LatestMessageID(context.Context, string) (string, error)
	ListStreamMessages(context.Context, string, string, string, int) ([]model.Message, error)
	GetMessageStates(context.Context, string, []string) ([]repo.MessageState, error)
	GetMessage(context.Context, string) (model.Message, error)
}

type messageReader struct {
	message model.Message
	cancel  context.CancelFunc
}
type messageDelivery struct {
	reader   *messageReader
	delivery agentuerunner.Delivery
	done     bool
}
type watchedMessage struct {
	message  model.Message // Only active messages remain in this map.
	revision uint64
	reader   *messageReader
	retryAt  time.Time
}

// ConvListener belongs to one stream request, never to an actor execution.
// +spec=`Only this Conv is observed. GC runs independently; terminal messages leave the active set.`
type ConvListener struct {
	repo           ConvMessageRepository
	events         agentuerunner.EventBridge
	conversationID string
}

func NewConvListener(events agentuerunner.EventBridge, repository ConvMessageRepository, conversationID string) *ConvListener {
	return &ConvListener{events: events, repo: repository, conversationID: conversationID}
}

func (listener *ConvListener) Run(ctx context.Context, deliver func(Event) error) error {
	conversationID := listener.conversationID
	watermark, err := listener.repo.LatestMessageID(ctx, conversationID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan messageDelivery)
	watching := map[string]*watchedMessage{}
	stop := func(watch *watchedMessage) {
		if watch.reader != nil {
			watch.reader.cancel()
			watch.reader = nil
		}
	}
	start := func(watch *watchedMessage) {
		if watch.reader != nil || time.Now().Before(watch.retryAt) {
			return
		}
		watch.retryAt = time.Now().Add(time.Second)
		readerCtx, cancelReader := context.WithCancel(ctx)
		reader := &messageReader{message: watch.message, cancel: cancelReader}
		watch.reader = reader
		go func() {
			defer cancelReader()
			send := func(value messageDelivery) error {
				select {
				case incoming <- value:
					return nil
				case <-readerCtx.Done():
					return readerCtx.Err()
				}
			}
			// Readers never provision Redis keys. Missing streams are repaired from SQL.
			_ = (agentuerunner.Replayer{Bridge: listener.events}).Stream(readerCtx, "message/"+reader.message.ID, "", func(value agentuerunner.Delivery) error {
				return send(messageDelivery{reader: reader, delivery: value})
			})
			_ = send(messageDelivery{reader: reader, done: true})
		}()
	}
	snapshot := func(row model.Message, watch *watchedMessage) error {
		revision := row.Revision
		if revision == 0 {
			revision = 1
		}
		if watch.revision >= revision {
			return nil
		}
		patch, err := agentueui.Start(row.Content, revision)
		if err != nil {
			return err
		}
		data, err := patch.Marshal()
		if err != nil {
			return err
		}
		message := visibleMessage(row)
		if err := deliver(Event{MessageID: row.ID, Message: &message, Data: data}); err != nil {
			return err
		}
		watch.revision = revision
		if row.Purpose == "output" && message.Ended() {
			data, err := agentueui.End(revision).Marshal()
			if err != nil {
				return err
			}
			return deliver(Event{MessageID: row.ID, Message: &message, Data: data})
		}
		return nil
	}
	observe := func(row model.Message) error {
		watch := watching[row.ID]
		if watch == nil {
			watch = &watchedMessage{}
		}
		if err := snapshot(row, watch); err != nil {
			return err
		}
		watch.message = row
		if !visibleMessage(row).Ended() || row.HumanDueAt != nil {
			watching[row.ID] = watch
			if row.Purpose == "output" && !visibleMessage(row).Ended() {
				start(watch)
			}
		} else {
			stop(watch)
			delete(watching, row.ID)
		}
		return nil
	}
	afterID := ""
	discover := func() (int, error) {
		// UUIDv7 is an allocation order, not a distributed commit watermark.
		// This uses the existing conversation cursor assumption, not Kafka's stronger guarantee.
		rows, err := listener.repo.ListStreamMessages(ctx, conversationID, afterID, watermark, discoveryPageSize)
		if err != nil {
			return 0, err
		}
		for _, row := range rows {
			if err := observe(row); err != nil {
				return 0, err
			}
			afterID = row.ID
		}
		return len(rows), nil
	}
	refreshActive := func() error {
		ids := make([]string, 0, len(watching))
		for id := range watching {
			ids = append(ids, id)
		}
		for from := 0; from < len(ids); from += discoveryPageSize {
			batch := ids[from:min(from+discoveryPageSize, len(ids))]
			states, err := listener.repo.GetMessageStates(ctx, conversationID, batch)
			if err != nil {
				return err
			}
			found := map[string]bool{}
			for _, state := range states {
				found[state.ID] = true
				watch := watching[state.ID]
				if state.Revision > watch.revision || state.Status != visibleMessage(watch.message).Status {
					row, err := listener.repo.GetMessage(ctx, state.ID)
					if errors.Is(err, repo.ErrNotFound) {
						stop(watch)
						delete(watching, state.ID)
						continue
					}
					if err != nil {
						return err
					}
					if err := observe(row); err != nil {
						return err
					}
				} else if state.Ended && state.HumanDueAt == nil {
					stop(watch)
					delete(watching, state.ID)
				} else if watch.message.Purpose == "output" {
					start(watch)
				}
			}
			for _, id := range batch {
				if !found[id] {
					stop(watching[id])
					delete(watching, id)
				}
			}
		}
		return nil
	}
	// Conv subscriptions bootstrap active outputs and pending Human cards only.
	// Completed history belongs to the paginated messages endpoint.
	for {
		count, err := discover()
		if err != nil {
			return err
		}
		if count < discoveryPageSize {
			break
		}
	}
	if afterID < watermark {
		afterID = watermark
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	ping, _ := agentueui.Ping(0).Marshal()
	if err := deliver(Event{Data: ping}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			if err := deliver(Event{Data: ping}); err != nil {
				return err
			}
		case <-ticker.C:
			if _, err := discover(); err != nil {
				return err
			}
			if err := refreshActive(); err != nil {
				return err
			}
		case value := <-incoming:
			id := value.reader.message.ID
			watch := watching[id]
			// Drop queued events from readers cancelled after a SQL terminal snapshot.
			if watch == nil || watch.reader != value.reader {
				continue
			}
			if value.done {
				stop(watch)
				continue
			}
			message := visibleMessage(watch.message)
			patch, err := agentueui.Parse(value.delivery.Data)
			if err != nil {
				return err
			}

			if patch.Op == agentueui.OpEnd || (patch.Op == agentueui.OpStart && patch.Seq > watch.revision) {
				// A bridge rebuilt from SQL may already represent a terminal
				// snapshot. Its AgentUE Start alone cannot carry Message.status.
				row, err := listener.repo.GetMessage(ctx, id)
				if err != nil {
					return err
				}
				if err := observe(row); err != nil {
					return err
				}
				continue
			}
			if patch.Op == agentueui.OpPing || patch.Seq <= watch.revision {
				continue
			}
			// Live deltas may overlap a newer SQL snapshot, but cannot jump a gap.
			if patch.Op != agentueui.OpStart && patch.Seq != watch.revision+1 {
				continue
			}
			watch.revision = patch.Seq
			event := Event{MessageID: id, Message: &message, Data: value.delivery.Data}
			if err := deliver(event); err != nil {
				return err
			}
		}
	}
}

func visibleMessage(m model.Message) contract.Message {
	return contract.Message{Status: contract.MessageStatus(m.Status), TargetKind: m.TargetKind, TargetKey: m.TargetKey, ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, Kind: m.Kind, Key: m.ActorKey, Content: m.Content, ReplyToID: m.ReplyToID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: contract.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}
