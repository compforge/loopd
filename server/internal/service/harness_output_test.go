package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/compforge/loopd/server/internal/component"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/repo"
)

func TestHarnessRedisKeepsAppendEventsWhileSQLKeepsMergedSnapshot(t *testing.T) {
	store, producer, _ := outputFixture(t)
	ctx := context.Background()
	run, err := store.CreateHarnessRun(ctx, contract.HarnessRunRequest{ConversationID: "work", IdempotencyKey: "stream", EffectKey: "plan", Target: "test", Text: "hello", Actor: &contract.ActorRef{Kind: contract.ActorKindHarness, Key: "test"}, Timeout: time.Minute, Meta: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Lock(ctx, repo.HarnessResource(run.ID), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := store.GetMessage(ctx, run.MessageID)
	if err := producer.ensureStream(ctx, m); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"hello", " world"} {
		event := ui.Event{Op: ui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "text", "type": "text", "content": text}}
		if err := store.AppendHarnessOutput(ctx, token, run.ID, uint64(i+1), "hash", event); err != nil {
			t.Fatal(err)
		}
		event.Seq = uint64(i + 2)
		producer.PublishCommitted(ctx, run.MessageID, event)
	}
	result := contract.TextResult("hello world")
	events, err := store.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		producer.PublishCommitted(ctx, run.MessageID, event)
	}
	records, err := producer.events.Read(ctx, "message/"+run.MessageID, "0-0")
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	var deltas []string
	for _, record := range records {
		event, err := ui.Parse(record.Data)
		if err != nil {
			t.Fatal(err)
		}
		ops = append(ops, string(event.Op))
		if event.Op == ui.OpAppend {
			deltas = append(deltas, event.Block["content"].(string))
		}
	}
	if strings.Join(ops, ",") != "start,append,append,set,end" || strings.Join(deltas, "|") != "hello| world" {
		t.Fatalf("Redis lost event history: ops=%v deltas=%v", ops, deltas)
	}
	m, err = store.GetMessage(ctx, run.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m.Content), `"op"`) || !strings.Contains(string(m.Content), "hello world") {
		t.Fatalf("DB is not a merged model: %s", m.Content)
	}
	// With the bridge removed, a new observer starts at the SQL snapshot and ends.
	if err := producer.events.Delete(ctx, "message/"+run.MessageID); err != nil {
		t.Fatal(err)
	}
	var observed []ui.Event
	if err := component.NewMessageListener(producer.events, store, run.MessageID, nil).Run(ctx, func(event ui.Event) error { observed = append(observed, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 2 || observed[0].Op != ui.OpStart || observed[1].Op != ui.OpEnd {
		t.Fatalf("snapshot recovery=%+v", observed)
	}
}

// Both public stream scopes follow the same Harness output through Redis.
func TestHarnessOutputObservedByRunAndConvListeners(t *testing.T) {
	store, producer, _ := outputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	run, err := store.CreateHarnessRun(ctx, contract.HarnessRunRequest{ConversationID: "work", IdempotencyKey: "both", EffectKey: "plan", Target: "test", Text: "hello", Actor: &contract.ActorRef{Kind: contract.ActorKindHarness, Key: "test"}, Timeout: time.Minute, Meta: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.Lock(ctx, repo.HarnessResource(run.ID), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	message, err := store.GetMessage(ctx, run.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.ensureStream(ctx, message); err != nil {
		t.Fatal(err)
	}
	runEvents, convEvents := make(chan ui.Event, 16), make(chan ui.Event, 16)
	runDone, convDone := make(chan error, 1), make(chan error, 1)
	go func() {
		runDone <- component.NewMessageListener(producer.events, store, run.MessageID, nil).Run(ctx, func(event ui.Event) error {
			select {
			case runEvents <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	go func() {
		convDone <- component.NewConvListener(producer.events, store, "work").Run(ctx, func(event component.Event) error {
			if event.MessageID != run.MessageID {
				return nil
			}
			value, err := ui.Parse(event.Data)
			if err != nil {
				return err
			}
			select {
			case convEvents <- value:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	defer func() { cancel(); <-convDone }()
	defer func() { cancel(); <-runDone }()
	await := func(events <-chan ui.Event, op ui.Op) {
		t.Helper()
		for {
			select {
			case event := <-events:
				if event.Op == op {
					return
				}
			case <-ctx.Done():
				t.Fatalf("missing %s event: %v", op, ctx.Err())
			}
		}
	}
	await(runEvents, ui.OpStart)
	await(convEvents, ui.OpStart)
	event := ui.Event{Op: ui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}
	if err := store.AppendHarnessOutput(ctx, token, run.ID, 1, "hash", event); err != nil {
		t.Fatal(err)
	}
	event.Seq = 2
	producer.PublishCommitted(ctx, run.MessageID, event)
	await(runEvents, ui.OpAppend)
	await(convEvents, ui.OpAppend)
	result := contract.TextResult("hello")
	events, err := store.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		producer.PublishCommitted(ctx, run.MessageID, event)
	}
	await(runEvents, ui.OpEnd)
	await(convEvents, ui.OpEnd)
	// A per-Run stream finishes. Conv remains live until its own request ends.
	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		runDone <- err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-convDone:
		convDone <- err
		t.Fatalf("Conv ended with a Run: %v", err)
	default:
	}
}
