package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

func TestHumanTerminalTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	request := contract.HumanRequest{EffectKey: "ask", Type: "ask", Title: "Question", Prompt: "Reply", AllowOther: true, Timeout: time.Minute}
	reply := contract.HumanReply{ReplyToID: "question", Outcome: contract.HumanSuccess, Value: "yes"}
	q := NewHumanQuestion(request, now)
	if q.Expire(now.Add(time.Second)) {
		t.Fatal("expired early")
	}

	if changed, err := q.Resolve(reply, "alice", nil); !changed || err != nil {
		t.Fatalf("resolve=%v %v", changed, err)
	}
	if q.Expire(now.Add(time.Hour)) {
		t.Fatal("changed terminal")
	}
	previous := &HumanAnswer{Actor: "alice", Outcome: reply.Outcome, Value: reply.Value}
	if changed, err := q.Resolve(reply, "alice", previous); changed || err != nil {
		t.Fatalf("retry=%v %v", changed, err)
	}
	reply.Value = "no"
	if _, err := q.Resolve(reply, "alice", previous); !errors.Is(err, ErrHumanConflict) {
		t.Fatal(err)
	}
	expired := NewHumanQuestion(request, now)
	if !expired.Expire(now.Add(time.Minute)) {
		t.Fatal("deadline inclusive")
	}
	if _, err := expired.Resolve(reply, "alice", nil); !errors.Is(err, ErrHumanConflict) {
		t.Fatal(err)
	}

}
