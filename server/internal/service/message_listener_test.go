package service

import (
	"context"
	"errors"
	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/component"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/compforge/loopd/server/internal/repo"
	"testing"
	"time"
)

type activeQueryCounter struct {
	*repo.Store
	t                         *testing.T
	activeID                  string
	checks, reads, firstReads int
}

func (counter *activeQueryCounter) GetMessage(ctx context.Context, id string) (model.Message, error) {
	counter.reads++
	return counter.Store.GetMessage(ctx, id)
}

func (counter *activeQueryCounter) GetMessageStates(ctx context.Context, convID string, ids []string) ([]repo.MessageState, error) {
	counter.checks++
	if len(ids) != 1 || ids[0] != counter.activeID {
		counter.t.Fatalf("watch includes ended history: %v", ids)
	}
	if counter.checks == 1 {
		counter.firstReads = counter.reads
	}
	if counter.checks == 2 {
		if counter.reads != counter.firstReads {
			counter.t.Fatal("unchanged message body reloaded")
		}
		return nil, errStop
	}
	return counter.Store.GetMessageStates(ctx, convID, ids)
}

func TestConversationStreamOnlyChecksActiveMetadata(t *testing.T) {
	store, producer, _ := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	active, err := store.Speak(ctx, "root", outputRequest("active"))
	if err != nil {
		t.Fatal(err)
	}
	old := outputRequest("ended")
	old.Status = contract.MessageStatusCompleted
	if _, err := store.Speak(ctx, "root", old); err != nil {
		t.Fatal(err)
	}
	counter := &activeQueryCounter{Store: store, t: t, activeID: active.ID}
	err = component.NewConvListener(producer.events, counter, "root").Run(ctx, func(Event) error { return nil })
	if !errors.Is(err, errStop) || counter.checks != 2 {
		t.Fatalf("checks=%d err=%v", counter.checks, err)
	}
}

// +case=`Conv stream skips terminal history and children; expiry ends a Message, not the subscription.`
func TestConversationStreamScopeDiscoveryAndExpiry(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	old := outputRequest("old")
	old.Status = contract.MessageStatusCompleted
	history, err := store.Speak(ctx, "root", old)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Speak(ctx, "work", outputRequest("child"))
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Speak(ctx, "root", outputRequest("active"))
	if err != nil {
		t.Fatal(err)
	}
	var next model.Message
	expired := false
	err = listen(ctx, consumer, "root", func(event Event) error {
		if event.Message == nil {
			return nil
		}
		if event.MessageID == history.ID || event.MessageID == child.ID || event.Message.Purpose == "input" {
			t.Fatal("loaded terminal history or child conv")
		}
		patch, err := ui.Parse(event.Data)
		if err != nil {
			return err
		}
		if event.MessageID == active.ID && patch.Op == ui.OpStart && event.Message.Status == contract.MessageStatusStreaming {
			// A missing Redis key alone must not expire the SQL Message.
			if err := producer.events.Delete(ctx, streamKey(active)); err != nil {
				return err
			}
			row, err := store.GetMessage(ctx, active.ID)
			if err != nil || row.Status != "streaming" {
				t.Fatal("bridge deletion changed DB status")
			}
			_, err = store.ExpireMessages(ctx, time.Now().Add(time.Second), 100)
			return err
		}
		if event.MessageID == active.ID && patch.Op == ui.OpStart && event.Message.Ended() {
			if event.Message.Status != contract.MessageStatusExpired {
				t.Fatal("missing expired status")
			}
			expired = true
			next, err = store.Speak(ctx, "root", outputRequest("after-expiry"))
			return err
		}
		if event.MessageID == next.ID && expired {
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) || !expired {
		t.Fatalf("stream stopped prematurely: %v", err)
	}
}

type Event = component.Event

func listen(ctx context.Context, coordinator *MessageService, convID string, deliver func(Event) error) error {
	return component.NewConvListener(coordinator.events, coordinator.repo.(component.ConvMessageRepository), convID).Run(ctx, deliver)
}
