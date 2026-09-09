package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/lock"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/gorm"
)

func runFixture(t *testing.T) (*Store, model.HarnessRun, lock.Token) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	_, err := s.CreateConversation(ctx, model.Conversation{ID: "conv", ActorKind: contract.ActorKindOperator, ActorKey: "op"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateHarnessRun(ctx, contract.HarnessRunRequest{ConversationID: "conv", IdempotencyKey: "key", EffectKey: "work", Target: "test", Text: "do work", Actor: &contract.ActorRef{Kind: contract.ActorKindHarness, Key: "test"}, Timeout: time.Hour, Meta: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Lock(ctx, HarnessResource(run.ID), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return s, run, token
}

// +case=`Poll returns streaming snapshots without blocking later messages; subsequent content remains readable by ID after the consumer advances.`
func TestInboxIncludesStreamingHarnessMessage(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	if err := s.db.Model(&model.Message{}).Where("id = ?", run.MessageID).
		Updates(map[string]any{"kind": "operator/op/harness", "actor_key": "worker"}).Error; err != nil {
		t.Fatal(err)
	}
	later, err := s.Speak(ctx, "conv", contract.SpeakRequest{Key: "later", Actor: contract.ActorRef{Kind: "operator", Key: "other"}, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	assertInbox := func(want int) []model.Message {
		t.Helper()
		rows, err := s.ListInbox(ctx, "conv", "operator", "reader", "", 100)
		if err != nil || len(rows) != want {
			t.Fatalf("inbox=%+v err=%v want=%d", rows, err, want)
		}
		return rows
	}
	if rows := assertInbox(2); rows[0].Status != "streaming" {
		t.Fatalf("streaming snapshot=%+v", rows[0])
	}
	if err := s.AppendHarnessOutput(ctx, token, run.ID, 1, "first", ui.Event{Op: ui.OpSet, Block: map[string]any{"id": "text", "type": "text", "content": "partial"}}); err != nil {
		t.Fatal(err)
	}
	assertInbox(2)
	result := contract.TextResult("complete")
	if _, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, ""); err != nil {
		t.Fatal(err)
	}
	rows := assertInbox(2)
	if rows[0].ID != run.MessageID || rows[1].ID != later.ID || rows[0].Status != "completed" {
		t.Fatalf("completed prefix=%+v", rows)
	}
	advanced, err := s.ListInbox(ctx, "conv", "operator", "reader", later.ID, 100)
	if err != nil || len(advanced) != 0 {
		t.Fatalf("ID discovery replayed a revision: %+v %v", advanced, err)
	}
	final, err := s.GetMessage(ctx, run.MessageID)
	if err != nil || final.Status != "completed" {
		t.Fatalf("retained ID cannot read completion: %+v %v", final, err)
	}
	if err := s.DeleteMessage(ctx, run.MessageID); !errors.Is(err, ErrConflict) {
		t.Fatalf("run-owned deletion=%v", err)
	}
}

func TestHarnessKindDoesNotImplyRunOwnership(t *testing.T) {
	s, _, _ := runFixture(t)
	ctx := context.Background()
	message, err := s.Speak(ctx, "conv", contract.SpeakRequest{Key: "speech", Actor: contract.ActorRef{Kind: "operator/op/harness", Key: "worker"}, Status: contract.MessageStatusStreaming, Content: json.RawMessage(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProjectOutput(ctx, message.ID, ui.Event{Op: ui.OpSet, Seq: 2, Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireMessages(ctx, time.Now().Add(time.Hour), 100)
	if err != nil || len(expired) != 1 || expired[0] != message.ID {
		t.Fatalf("ordinary Harness speech expiry=%v err=%v", expired, err)
	}
}
func TestHarnessLeaseTakeoverFencesEveryWrite(t *testing.T) {
	s, run, old := runFixture(t)
	ctx := context.Background()
	if _, err := s.Lock(ctx, old.Resource, time.Minute); !errors.Is(err, lock.ErrLocked) {
		t.Fatalf("occupied=%v", err)
	}
	if err := s.db.Model(&model.ResourceLock{}).Where("resource = ?", old.Resource).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Renew(ctx, old); !errors.Is(err, lock.ErrLost) {
		t.Fatalf("expired renew=%v", err)
	}
	var wg sync.WaitGroup
	tokens := make(chan lock.Token, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.Lock(ctx, old.Resource, time.Minute)
			if err == nil {
				tokens <- token
			} else {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(tokens)
	close(errs)
	if len(tokens) != 1 || len(errs) != 1 {
		t.Fatalf("winners=%d losers=%d", len(tokens), len(errs))
	}
	winner := <-tokens
	for err := range errs {
		if !errors.Is(err, lock.ErrLocked) {
			t.Fatal(err)
		}
	}
	event := ui.Event{Op: ui.OpAppend, Mask: "block.content", Block: map[string]any{"id": "text", "type": "text", "content": "hello"}}
	if err := s.AppendHarnessOutput(ctx, old, run.ID, 1, "hash", event); !errors.Is(err, lock.ErrLost) {
		t.Fatalf("stale output=%v", err)
	}
	if _, err := s.FinishHarnessRun(ctx, old, run.ID, contract.CallFailed, nil, "old"); !errors.Is(err, lock.ErrLost) {
		t.Fatalf("stale finish=%v", err)
	}
	if err := s.Unlock(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Renew(ctx, winner); err != nil {
		t.Fatalf("old release deleted winner: %v", err)
	}
	if err := s.AppendHarnessOutput(ctx, winner, run.ID, 1, "hash", event); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendHarnessOutput(ctx, winner, run.ID, 1, "hash", event); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetMessage(ctx, run.MessageID)
	if strings.Count(string(m.Content), "hello") != 1 {
		t.Fatalf("duplicate append: %s", m.Content)
	}
	if err := s.ProjectOutput(ctx, m.ID, ui.Event{Op: ui.OpSet, Seq: m.Revision + 1, Block: map[string]any{"id": "text", "type": "text", "content": "intruder"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unfenced writer=%v", err)
	}
}
func TestHarnessResultAndTerminalCommitTogetherIncludingParts(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	s.messageInlineBlocks = 1
	s.messageInlineBytes = 128
	s.messagePartBytes = 128
	event := ui.Event{Op: ui.OpSet, Block: map[string]any{"id": "progress", "type": "text", "content": "working"}}
	if err := s.AppendHarnessOutput(ctx, token, run.ID, 1, "hash", event); err != nil {
		t.Fatal(err)
	}
	result := contract.HarnessResult{Format: "json", Content: json.RawMessage(`{"artifact":"` + strings.Repeat("x", 10000) + `"}`)}
	inject := errors.New("simulated commit failure")
	if err := s.db.Callback().Update().Before("gorm:update").Register("test:fail_run_terminal", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "harness_runs" {
			if updates, ok := tx.Statement.Dest.(map[string]any); ok && updates["phase"] == "succeeded" {
				tx.AddError(inject)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, ""); !errors.Is(err, inject) {
		t.Fatalf("injected=%v", err)
	}
	_ = s.db.Callback().Update().Remove("test:fail_run_terminal")
	m, _ := s.GetMessage(ctx, run.MessageID)
	value, err := contract.ExtractResult(m.Content)
	if err != nil || value != nil || m.Status != "streaming" {
		t.Fatalf("uncommitted result escaped: %+v %s %v", value, m.Status, err)
	}
	events, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Op != ui.OpSet || events[0].Block["type"] != "result" || events[1].Op != ui.OpEnd {
		t.Fatalf("committed events=%+v", events)
	}
	m, err = s.GetMessage(ctx, run.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	value, err = contract.ExtractResult(m.Content)
	if err != nil || value.Text() != result.Text() || m.Status != "completed" {
		t.Fatalf("result=%+v status=%s err=%v", value, m.Status, err)
	}
	var stored model.Message
	_ = s.db.First(&stored, "id = ?", m.ID).Error
	if !strings.Contains(string(stored.Content), `"ref"`) {
		t.Fatal("result did not exercise physical parts")
	}
	current, _ := s.GetHarnessRun(ctx, run.ID)
	if current.Phase != "succeeded" || current.Checkpoint != 1 {
		t.Fatalf("run=%+v", current)
	}
}
func TestHarnessCancelWinsCompletion(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	if err := s.CancelHarnessRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	value := contract.TextResult("too late")
	if _, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &value, ""); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetHarnessRun(ctx, run.ID)
	m, _ := s.GetMessage(ctx, run.MessageID)
	result, _ := contract.ExtractResult(m.Content)
	if current.Phase != "cancelled" || result != nil || m.Status != "cancelled" {
		t.Fatalf("late result=%+v run=%+v", result, current)
	}
}

// Run output cannot be rewritten or removed through ordinary message maintenance,
// either while active or after completion: the persisted result belongs to the call.
func TestHarnessOwnedMessageRejectsOrdinaryReplacementAndDeletion(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	for _, terminal := range []bool{false, true} {
		if terminal {
			result := contract.TextResult("durable result")
			if _, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, ""); err != nil {
				t.Fatal(err)
			}
		}
		before, err := s.GetMessage(ctx, run.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateMessageContent(ctx, run.ConversationID, run.MessageID, []byte(`{"version":"1.1","biz":"chat","meta":{},"blocks":[]}`)); !errors.Is(err, ErrConflict) {
			t.Fatalf("replace terminal=%v: %v", terminal, err)
		}
		if err := s.DeleteMessage(ctx, run.MessageID); !errors.Is(err, ErrConflict) {
			t.Fatalf("delete terminal=%v: %v", terminal, err)
		}
		after, err := s.GetMessage(ctx, run.MessageID)
		if err != nil || string(before.Content) != string(after.Content) || before.Revision != after.Revision {
			t.Fatalf("protected message changed: %v", err)
		}
	}
}

// A long silent Harness execution remains writable until its own deadline.
func TestMessageGCLeavesHarnessOutputToRunDeadline(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-48 * time.Hour)
	if err := s.db.Model(&model.Message{}).Where("id = ?", run.MessageID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireMessages(ctx, time.Now().UTC().Add(-24*time.Hour), 100)
	if err != nil || len(expired) != 0 {
		t.Fatalf("Harness message expired independently: %v %v", expired, err)
	}
	result := contract.TextResult("late result")
	if _, err := s.FinishHarnessRun(ctx, token, run.ID, contract.CallSucceeded, &result, ""); err != nil {
		t.Fatal(err)
	}
	message, err := s.GetMessage(ctx, run.MessageID)
	if err != nil || message.Status != "completed" {
		t.Fatalf("final=%+v err=%v", message, err)
	}
}

func TestDueHarnessRunsIgnoreRetryBackoffButRespectLeases(t *testing.T) {
	s, run, token := runFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.db.Model(&model.HarnessRun{}).Where("id = ?", run.ID).Updates(map[string]any{"next_attempt_at": now.Add(time.Hour), "deadline_at": now.Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	due, err := s.ListDueHarnessRuns(ctx, 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("live lease selected: %v %v", due, err)
	}
	if err := s.db.Model(&model.ResourceLock{}).Where("resource = ?", token.Resource).Update("expires_at", now.Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	due, err = s.ListDueHarnessRuns(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != run.ID {
		t.Fatalf("expired recovery hidden by backoff: %v %v", due, err)
	}
	if err := s.db.Model(&model.HarnessRun{}).Where("id = ?", run.ID).Updates(map[string]any{"deadline_at": now.Add(time.Hour), "cancel_requested": true}).Error; err != nil {
		t.Fatal(err)
	}
	due, err = s.ListDueHarnessRuns(ctx, 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("cancellation hidden by backoff: %v %v", due, err)
	}
	if err := s.db.Model(&model.HarnessRun{}).Where("id = ?", run.ID).Update("phase", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	due, err = s.ListDueHarnessRuns(ctx, 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("terminal run selected: %v %v", due, err)
	}
}
