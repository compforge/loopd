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
	if _, err := s.CreateChatInput(ctx, model.Message{ID: "input", ConversationID: "conv", TaskID: "task", Kind: "user", ActorKey: "alice", Content: content}); err != nil {
		t.Fatal(err)
	}
	return s
}
func question(key string) loopd.HumanRequest {
	return loopd.HumanRequest{ConversationID: "conv", Actor: loopd.ActorRef{Kind: loopd.ActorKindOperator, Key: "operator"}, Target: loopd.ActorRef{Kind: loopd.ActorKindUser, Key: "alice"}, ReplyToID: "input", EffectKey: key, Type: "ask", Title: "Scope", Prompt: "Choose", Timeout: time.Hour, Choices: []loopd.HumanChoice{{Value: "small", Label: "Small"}, {Value: "full", Label: "Full"}}}
}

// +case=`两个并行问题按相反顺序答复，只按引用收口且 输入消息保持原身份`
func TestHumanParallelReplyAndIdentity(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	r := question("scope")
	ask, err := s.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	confirmReq := question("budget")
	confirmReq.Type = "confirm"
	confirmReq.Choices = nil
	confirm, err := s.CreateHuman(ctx, confirmReq)
	if err != nil {
		t.Fatal(err)
	}
	if ask.Message.Key != "operator" || ask.Message.ReplyToID != "input" {
		t.Fatalf("message=%+v", ask.Message)
	}
	for _, test := range []struct {
		conv, actor, id, value string
		want                   error
	}{
		{"other", "alice", ask.Message.ID, "small", ErrNotFound},
		{"conv", "mallory", ask.Message.ID, "small", ErrForbidden},
		{"conv", "alice", "missing", "small", ErrNotFound},
		{"conv", "alice", ask.Message.ID, "outside", ErrInvalidHuman},
	} {
		_, err := s.ReplyHuman(ctx, test.conv, test.actor, loopd.HumanReply{ReplyToID: test.id, Outcome: loopd.HumanSuccess, Value: test.value})
		if !errors.Is(err, test.want) {
			t.Fatalf("%+v: %v", test, err)
		}
	}
	reply := loopd.HumanReply{ReplyToID: confirm.Message.ID, Outcome: loopd.HumanSuccess, Value: "declined"}
	answered, err := s.ReplyHuman(ctx, "conv", "alice", reply)
	if err != nil {
		t.Fatal(err)
	}
	if answered.Status != loopd.HumanSuccess || answered.Value != "declined" || answered.Reply.ReplyToID != confirm.Message.ID {
		t.Fatalf("result=%+v", answered)
	}
	again, err := s.ReplyHuman(ctx, "conv", "alice", reply)
	if err != nil || again.Reply.ID != answered.Reply.ID {
		t.Fatalf("repeat=%+v %v", again, err)
	}
	reply.Value = "accepted"
	if _, err := s.ReplyHuman(ctx, "conv", "alice", reply); !errors.Is(err, ErrConflict) {
		t.Fatalf("contradictory reply=%v", err)
	}
	askAnswer, err := s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: ask.Message.ID, Outcome: loopd.HumanSuccess, Value: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if askAnswer.Reply.ReplyToID != ask.Message.ID {
		t.Fatalf("reply=%+v", askAnswer.Reply)
	}
	same, err := s.CreateHuman(ctx, r)
	if err != nil || same.Message.ID != ask.Message.ID || same.Deadline != ask.Deadline || same.Value != "full" {
		t.Fatalf("replay=%+v %v", same, err)
	}
	r.Timeout++
	if _, err := s.CreateHuman(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed params=%v", err)
	}
	rows, _ := s.ListMessages(ctx, "conv", "", 100)
	input, err := s.GetDeliveryInput(ctx, "task")
	if err != nil || input.ID != "input" || len(rows) != 5 {
		t.Fatalf("input=%+v rows=%d %v", input, len(rows), err)
	}
	if _, err := s.CreateHuman(ctx, question("new")); err != nil {
		t.Fatalf("answered questions must not prevent a new question: %v", err)
	}
	// Human notification cleanup belongs to its own maintenance loop, not Chat.
}

// +case=`deadline 与用户答复竞争不覆盖终态；timeout 不伪造用户消息`
func TestHumanTimeoutDismissAndFailure(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	r := question("expired")
	r.Timeout = time.Nanosecond
	q, err := s.CreateHuman(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: "small"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("late reply=%v", err)
	}
	result, err := s.GetHuman(ctx, q.Message.ID)
	if err != nil || result.Status != loopd.HumanTimeout || result.Reply != nil {
		t.Fatalf("timeout=%+v %v", result, err)
	}
	dismiss, err := s.CreateHuman(ctx, question("dismiss"))
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: dismiss.Message.ID, Outcome: loopd.HumanDismissed})
	if err != nil || result.Status != loopd.HumanDismissed || result.Value != "" {
		t.Fatalf("dismiss=%+v %v", result, err)
	}
	pending, err := s.CreateHuman(ctx, question("pending"))
	if err != nil {
		t.Fatal(err)
	}
	result, err = s.GetHuman(ctx, pending.Message.ID)
	if err != nil || result.Status != loopd.HumanPending || result.Reason != "" || result.Reply != nil {
		t.Fatalf("failure=%+v %v", result, err)
	}
	rows, _ := s.ListMessages(ctx, "conv", "", 100)
	if len(rows) != 5 {
		t.Fatalf("rows=%d, expected one input, three questions and one dismissal", len(rows))
	}
}
func TestConcurrentHumanRepliesHaveOneWinner(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	q, err := s.CreateHuman(ctx, question("race"))
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
			_, err := s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: value})
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
	rows, _ := s.ListMessages(ctx, "conv", "", 100)
	if len(rows) != 3 {
		t.Fatalf("got %d messages after competing replies", len(rows))
	}
}
func TestHumanRecoversFromDatabaseAndKeepsWake(t *testing.T) {
	s := humanStore(t)
	ctx := context.Background()
	q, err := s.CreateHuman(ctx, question("recover"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplyHuman(ctx, "conv", "alice", loopd.HumanReply{ReplyToID: q.Message.ID, Outcome: loopd.HumanSuccess, Value: "small"}); err != nil {
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
	same, err := other.CreateHuman(ctx, question("recover"))
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
