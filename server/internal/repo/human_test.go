package repo

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"gorm.io/driver/sqlite"
)

func humanStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "human.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.CreateConversation(ctx, model.Conversation{ID: "conv"}); err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"version":"1.0","biz":"chat","meta":{},"blocks":[]}`)
	if _, err := s.CreateChatMessages(ctx, model.Message{ID: "input", ConversationID: "conv", TaskID: "task", Kind: "user", ActorKey: "alice", Content: content}, model.Message{ID: "response", ConversationID: "conv", TaskID: "task", Kind: "operator", ActorKey: "operator", Content: content}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}
func question(key string) loopd.HumanRequest {
	return loopd.HumanRequest{TaskID: "task", EffectKey: key, Type: "ask", Title: "Scope", Prompt: "Choose", Timeout: time.Hour, Choices: []loopd.HumanChoice{{Value: "small", Label: "Small"}, {Value: "full", Label: "Full"}}}
}

// +case=`两个并行问题按相反顺序答复，只按引用收口且 Task 主消息保持原身份`
func TestHumanParallelReplyAndIdentity(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	r := question("scope")
	ask, err := s.CreateHuman(ctx, r, true)
	if err != nil {
		t.Fatal(err)
	}
	confirmReq := question("budget")
	confirmReq.Type = "confirm"
	confirmReq.Choices = nil
	confirm, err := s.CreateHuman(ctx, confirmReq, true)
	if err != nil {
		t.Fatal(err)
	}
	if ask.Message.Key != "operator" || ask.Message.ReplyToMessageID != "input" {
		t.Fatalf("message=%+v", ask.Message)
	}
	if err := s.BeginCompletion(ctx, "task", []byte("null"), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("complete pending=%v", err)
	}
	for _, test := range []struct {
		conv, task, actor, id, value string
		want                         error
	}{
		{"other", "task", "alice", ask.Message.ID, "small", ErrNotFound},
		{"conv", "missing", "alice", ask.Message.ID, "small", ErrNotFound},
		{"conv", "task", "mallory", ask.Message.ID, "small", ErrForbidden},
		{"conv", "task", "alice", "missing", "small", ErrNotFound},
		{"conv", "task", "alice", ask.Message.ID, "outside", ErrInvalidHuman},
	} {
		_, err := s.ReplyHuman(ctx, test.conv, test.task, test.actor, loopd.HumanReply{ReplyToMessageID: test.id, Outcome: loopd.HumanSuccess, Value: test.value})
		if !errors.Is(err, test.want) {
			t.Fatalf("%+v: %v", test, err)
		}
	}
	reply := loopd.HumanReply{ReplyToMessageID: confirm.Message.ID, Outcome: loopd.HumanSuccess, Value: "declined"}
	answered, err := s.ReplyHuman(ctx, "conv", "task", "alice", reply)
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != loopd.HumanSuccess || answered.Value != "declined" || answered.Reply.ReplyToMessageID != confirm.Message.ID {
		t.Fatalf("result=%+v", answered)
	}
	again, err := s.ReplyHuman(ctx, "conv", "task", "alice", reply)
	if err != nil || again.Reply.ID != answered.Reply.ID {
		t.Fatalf("repeat=%+v %v", again, err)
	}
	reply.Value = "accepted"
	if _, err := s.ReplyHuman(ctx, "conv", "task", "alice", reply); !errors.Is(err, ErrConflict) {
		t.Fatalf("contradictory reply=%v", err)
	}
	askAnswer, err := s.ReplyHuman(ctx, "conv", "task", "alice", loopd.HumanReply{ReplyToMessageID: ask.Message.ID, Outcome: loopd.HumanSuccess, Value: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if askAnswer.Reply.ReplyToMessageID != ask.Message.ID {
		t.Fatalf("reply=%+v", askAnswer.Reply)
	}
	same, err := s.CreateHuman(ctx, r, true)
	if err != nil || same.Message.ID != ask.Message.ID || same.Deadline != ask.Deadline || same.Value != "full" {
		t.Fatalf("replay=%+v %v", same, err)
	}
	r.Timeout++
	if _, err := s.CreateHuman(ctx, r, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed params=%v", err)
	}
	rows, _ := s.ListRootMessagesByTask(ctx, "task")
	input, response, err := ChatMessages(rows)
	if err != nil || input.ID != "input" || response.ID != "response" || len(rows) != 6 {
		t.Fatalf("pair=%s/%s rows=%d %v", input.ID, response.ID, len(rows), err)
	}
	if err := s.BeginCompletion(ctx, "task", []byte("null"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHuman(ctx, question("new"), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("new after completion=%v", err)
	}
	if err := s.FinishCompletion(ctx, "task"); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.HumanMaintenance(ctx)
	if len(rows) != 0 {
		t.Fatalf("retired task has outstanding wakes: %+v", rows)
	}
}

// +case=`deadline 与用户答复竞争不覆盖终态；timeout 不伪造用户消息`
func TestHumanTimeoutDismissAndFailure(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	r := question("expired")
	r.Timeout = time.Nanosecond
	q, err := s.CreateHuman(ctx, r, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReplyHuman(ctx, "conv", "task", "alice", loopd.HumanReply{ReplyToMessageID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: "small"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("late reply=%v", err)
	}
	result, err := s.GetHuman(ctx, q.Message.ID)
	if err != nil || result.Status != loopd.HumanTimeout || result.Reply != nil {
		t.Fatalf("timeout=%+v %v", result, err)
	}
	dismiss, err := s.CreateHuman(ctx, question("dismiss"), true)
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ReplyHuman(ctx, "conv", "task", "alice", loopd.HumanReply{ReplyToMessageID: dismiss.Message.ID, Outcome: loopd.HumanDismissed})
	if err != nil || result.Status != loopd.HumanDismissed || result.Value != "" {
		t.Fatalf("dismiss=%+v %v", result, err)
	}
	pending, err := s.CreateHuman(ctx, question("pending"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCompletion(ctx, "task", []byte(`{"code":"stopped","message":"done"}`), true); err != nil {
		t.Fatal(err)
	}
	result, err = s.GetHuman(ctx, pending.Message.ID)
	if err != nil || result.Status != loopd.HumanFailure || result.Reason != "task_ended" || result.Reply != nil {
		t.Fatalf("failure=%+v %v", result, err)
	}
	rows, _ := s.ListRootMessagesByTask(ctx, "task")
	if len(rows) != 6 {
		t.Fatalf("rows=%d, expected two initial, three questions and one dismissal", len(rows))
	}
}
func TestConcurrentHumanRepliesHaveOneWinner(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	q, err := s.CreateHuman(ctx, question("race"), true)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			value := "small"
			if i%2 != 0 {
				value = "full"
			}
			_, err := s.ReplyHuman(ctx, "conv", "task", "alice", loopd.HumanReply{ReplyToMessageID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: value})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	rows, _ := s.ListRootMessagesByTask(ctx, "task")
	if len(rows) != 4 {
		t.Fatalf("got %d messages after competing replies", len(rows))
	}
}
func TestHumanRecoversFromDatabaseAndKeepsWake(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	q, err := s.CreateHuman(ctx, question("recover"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplyHuman(ctx, "conv", "task", "alice", loopd.HumanReply{ReplyToMessageID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: "small"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.HumanMaintenance(ctx)
	if err != nil || len(rows) != 1 || !rows[0].WakePending {
		t.Fatalf("outbox=%+v %v", rows, err)
	}
	// Close the database connection and reopen through the production migration path.
	dsn := s.db.Dialector.(*sqlite.Dialector).DSN
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(Config{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s = other
	same, err := other.CreateHuman(ctx, question("recover"), true)
	if err != nil || same.Message.ID != q.Message.ID || same.Value != "small" {
		t.Fatalf("recover=%+v %v", same, err)
	}
	if err := other.AcknowledgeHumanWake(ctx, q.Message.ID, rows[0].Revision-1); err != nil {
		t.Fatal(err)
	}
	still, _ := s.HumanMaintenance(ctx)
	if len(still) != 1 {
		t.Fatal("stale ack lost wake")
	}
	if err := other.AcknowledgeHumanWake(ctx, q.Message.ID, rows[0].Revision); err != nil {
		t.Fatal(err)
	}
	still, _ = s.HumanMaintenance(ctx)
	if len(still) != 0 {
		t.Fatal("wake not acknowledged")
	}
}
