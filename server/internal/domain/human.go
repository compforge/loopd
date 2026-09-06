// Package domain owns collaboration rules independently of storage.
package domain

import (
	"errors"
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

var ErrHumanConflict = errors.New("Human request already resolved")

// HumanQuestion is the state of one message-backed request, not a second persisted entity.
// Request input and deadline are immutable; the repository encodes this state in its Message.
type HumanQuestion struct {
	Request  contract.HumanRequest
	Status   contract.HumanStatus
	Deadline time.Time
	Reason   string
}
type HumanAnswer struct {
	Actor   string
	Outcome contract.HumanStatus
	Value   string
}

func NewHumanQuestion(request contract.HumanRequest, now time.Time) HumanQuestion {
	return HumanQuestion{Request: request, Status: contract.HumanPending, Deadline: now.Add(request.Timeout)}
}

// +spec=`到期只转换 pending；已收口的问题不能被重开或改写`
func (q *HumanQuestion) Expire(now time.Time) bool {
	if q.Status != contract.HumanPending || now.Before(q.Deadline) {
		return false
	}
	q.Status = contract.HumanTimeout
	return true
}

// Resolve is called after Expire under the same question transaction.
// +spec=`相同答复重试不产生新消息，矛盾答复与迟到答复拒绝`
func (q *HumanQuestion) Resolve(reply contract.HumanReply, actor string, previous *HumanAnswer) (bool, error) {
	if err := q.Request.ValidateReply(reply); err != nil {
		return false, err
	}
	if q.Status != contract.HumanPending {
		if previous != nil && previous.Actor == actor && previous.Outcome == reply.Outcome && previous.Value == reply.Value {
			return false, nil
		}
		return false, ErrHumanConflict
	}

	q.Status = reply.Outcome
	return true, nil
}
