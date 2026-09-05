package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	runner "github.com/compforge/agentue/sdks/go/runner"
	ui "github.com/compforge/agentue/sdks/go/ui"
	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/repo"
)

func TestOneShotSpeakNeedsNoMessageStream(t *testing.T) {
	store, producer, _ := outputFixture(t)
	ctx := context.Background()
	request := outputRequest("once")
	request.Stream = false
	message, err := store.Speak(ctx, "root", request)
	if err != nil {
		t.Fatal(err)
	}
	if !visibleMessage(message).Ended() {
		t.Fatal("one-shot message not ended")
	}
	same, err := store.Speak(ctx, "root", request)
	if err != nil || same.ID != message.ID {
		t.Fatalf("retry=%+v %v", same, err)
	}
	if _, err := producer.EmitMessage(ctx, message.ID, outputText(t, 2, "too late")); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("one-shot write=%v", err)
	}
	if _, err := producer.EmitMessage(ctx, message.ID, marshalEvent(t, ui.End(2))); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.events.State(ctx, streamKey(message)); !errors.Is(err, runner.ErrNotFound) {
		t.Fatalf("unnecessary message stream=%v", err)
	}
}

type interruptedBridge struct {
	runner.EventBridge
	fail bool
}

func (bridge *interruptedBridge) Publish(ctx context.Context, key string, data json.RawMessage, seq uint64) (string, error) {
	if bridge.fail {
		return "", errors.New("page bridge unavailable")
	}
	return bridge.EventBridge.Publish(ctx, key, data, seq)
}

// +case=`SQL acceptance survives bridge failure; gaps are repaired with a full model and conflicting retries cannot alter it.`
func TestMessagePublicationSurvivesBridgeFailure(t *testing.T) {
	store, producer, _ := outputFixture(t)
	ctx := context.Background()
	message, err := store.Speak(ctx, "work", outputRequest("bridge-outage"))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.ensureStream(ctx, message); err != nil {
		t.Fatal(err)
	}
	bridge := &interruptedBridge{EventBridge: producer.events, fail: true}
	writer := New(bridge, store, nil)
	update := outputText(t, 2, "accepted")
	for i := 0; i < 2; i++ {
		if _, err := writer.EmitMessage(ctx, message.ID, update); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.EmitMessage(ctx, message.ID, outputText(t, 2, "changed")); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("conflicting retry=%v", err)
	}
	saved, err := store.GetMessage(ctx, message.ID)
	if err != nil || saved.Revision != 2 || !strings.Contains(string(saved.Content), "accepted") {
		t.Fatalf("SQL acceptance: %+v %v", saved, err)
	}
	if !saved.UpdatedAt.After(saved.CreatedAt) {
		t.Fatal("an event without a timestamp must still record visible activity")
	}
	bridge.fail = false
	if _, err := writer.EmitMessage(ctx, message.ID, outputText(t, 3, "recovered")); err != nil {
		t.Fatal(err)
	}
	state, err := bridge.State(ctx, streamKey(message))
	if err != nil || state.LastSeq != 3 {
		t.Fatalf("bridge=%+v %v", state, err)
	}
	// Observe the actual bridge, not SQL discovery, to prove the missing delta
	// was repaired before the bridge advanced past it.
	watch, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err = (runner.Replayer{Bridge: bridge}).Stream(watch, streamKey(message), "", func(value runner.Delivery) error {
		event, err := ui.Parse(value.Data)
		if err != nil {
			return err
		}
		if event.Seq == 3 {
			if event.Op != ui.OpStart || !strings.Contains(string(value.Data), "recovered") {
				t.Fatalf("missing snapshot repair: %s", value.Data)
			}
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatal(err)
	}
}

type failingProjection struct {
	*repo.Store
	fail bool
}

func (store *failingProjection) ProjectOutput(ctx context.Context, id string, event ui.Event) error {
	if store.fail {
		store.fail = false
		return errors.New("projection temporarily unavailable")
	}
	return store.Store.ProjectOutput(ctx, id, event)
}

// +case=`End follows the same seq/retry contract as content; persisted End survives Redis loss and forbids further output.`
func TestEndRetriesProjectionAndSurvivesBridgeLoss(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx := context.Background()
	message, err := store.Speak(ctx, "work", outputRequest("stream"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := producer.EmitMessage(ctx, message.ID, outputText(t, 2, "hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.EmitMessage(ctx, message.ID, marshalEvent(t, ui.End(99))); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("gap accepted: %v", err)
	}
	flaky := New(producer.events, &failingProjection{Store: store, fail: true}, nil)
	end := marshalEvent(t, ui.End(3))
	if _, err := flaky.EmitMessage(ctx, message.ID, end); err == nil {
		t.Fatal("projection failure not reported")
	}
	if _, err := consumer.EmitMessage(ctx, message.ID, end); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.EmitMessage(ctx, message.ID, end); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetMessage(ctx, message.ID)
	if err != nil || saved.Revision != 3 || !visibleMessage(saved).Ended() {
		t.Fatalf("snapshot=%+v %v", saved, err)
	}
	if err := producer.events.Delete(ctx, streamKey(message)); err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.EmitMessage(ctx, message.ID, end); err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.EmitMessage(ctx, message.ID, outputText(t, 4, "too late")); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("write after end=%v", err)
	}
	watchCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err = consumer.Stream(watchCtx, "task", "root", "", func(event Event) error {
		if event.MessageID == message.ID {
			if !event.Message.Ended() || event.Message.Revision != 3 {
				t.Fatalf("lost end on replay: %+v", event.Message)
			}
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatal(err)
	}
}

// +case=`A message End never ends the page subscription; other actors and other/no TaskIDs remain visible.`
func TestSubscriptionContinuesAfterMessageEnd(t *testing.T) {
	store, producer, consumer := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := store.Speak(ctx, "work", outputRequest("first"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	sent, ended := false, false
	var later, unsolicited string
	err = consumer.Stream(ctx, "task", "root", "", func(event Event) error {
		patch, err := ui.Parse(event.Data)
		if err != nil {
			return err
		}
		if event.MessageID == "" && patch.Op == ui.OpEnd {
			t.Fatal("message End closed page")
		}
		if event.MessageID == first.ID && patch.Op == ui.OpStart && !sent {
			sent = true
			if _, err := producer.EmitMessage(ctx, first.ID, outputText(t, 2, "first")); err != nil {
				return err
			}
			_, err = producer.EmitMessage(ctx, first.ID, marshalEvent(t, ui.End(3)))
			return err
		}
		if event.MessageID == first.ID && patch.Op == ui.OpEnd && !ended {
			ended = true
			request := outputRequest("later")
			request.Stream = false
			message, err := store.Speak(ctx, "work", request)
			if err != nil {
				return err
			}
			later = message.ID
			request = outputRequest("unsolicited")
			request.Stream = false
			request.Actor = loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "another"}
			message, err = store.Speak(ctx, "root", request)
			if err != nil {
				return err
			}
			unsolicited = message.ID
		}
		if event.MessageID != "" {
			seen[event.MessageID] = true
		}
		if ended && seen[later] && seen[unsolicited] {
			return errStop
		}
		return nil
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("ended=%v seen=%v err=%v", ended, seen, err)
	}
}

func TestOutputMetadataCannotBeForged(t *testing.T) {
	for _, mask := range []string{"meta.output.ended", "meta.human.status"} {
		data := marshalEvent(t, ui.Event{Op: ui.OpSet, Seq: 2, Mask: mask, Meta: map[string]any{"ended": true, "status": "success"}})
		if _, err := parseOutputEvent(data); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("mask %s accepted: %v", mask, err)
		}
	}
}
