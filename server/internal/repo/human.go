package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/domain"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidHuman = errors.New("invalid Human request")
var ErrForbidden = errors.New("actor cannot answer this chat")

type humanContent struct {
	Version string `json:"version"`
	Biz     string `json:"biz"`
	Meta    struct {
		Human struct {
			EffectKey   string        `json:"effect_key"`
			Timeout     time.Duration `json:"timeout"`
			Fingerprint string        `json:"fingerprint"`
		} `json:"human"`
	} `json:"meta"`
	Blocks []contract.HumanBlock `json:"blocks"`
}

func decodeHuman(m model.Message) (humanContent, error) {
	var c humanContent
	if m.Purpose != "human_request" {
		return c, ErrNotFound
	}
	if err := json.Unmarshal(m.Content, &c); err != nil {
		return c, err
	}
	if len(c.Blocks) != 1 || (c.Blocks[0].Type != "ask" && c.Blocks[0].Type != "confirm") {
		return c, fmt.Errorf("corrupt Human message %s", m.ID)
	}
	return c, nil
}

func humanResult(tx *gorm.DB, m model.Message, c humanContent) (contract.HumanResult, error) {
	b := c.Blocks[0]
	result := contract.HumanResult{Message: publicMessage(m), Status: b.Status, Deadline: b.Deadline, Reason: b.Reason}
	if b.Status == contract.HumanSuccess || b.Status == contract.HumanDismissed {
		var reply model.Message
		if err := tx.First(&reply, "reply_to_id = ? AND purpose = ?", m.ID, "human_reply").Error; err != nil {
			return result, err
		}
		if err := hydrateMessage(tx, &reply); err != nil {
			return result, err
		}
		var content struct {
			Blocks []contract.HumanReplyBlock `json:"blocks"`
		}
		if err := json.Unmarshal(reply.Content, &content); err != nil {
			return result, err
		}
		if len(content.Blocks) != 1 {
			return result, fmt.Errorf("corrupt reply %s", reply.ID)
		}
		result.Value = content.Blocks[0].Value
		value := publicMessage(reply)
		result.Reply = &value
	}
	return result, nil
}
func publicMessage(m model.Message) contract.Message {
	return contract.Message{Status: contract.MessageStatus(m.Status), TargetKind: m.TargetKind, TargetKey: m.TargetKey, ID: m.ID, ConversationID: m.ConversationID, Kind: m.Kind, Key: m.ActorKey, Content: m.Content, ReplyToID: m.ReplyToID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: contract.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}
func (store *Store) saveHuman(tx *gorm.DB, m *model.Message, c humanContent, wake bool) error {
	content, err := json.Marshal(c)
	if err != nil {
		return err
	}
	m.Content = content
	m.Revision++
	m.HumanDueAt = nil
	m.WakePending = wake
	return store.saveMessage(tx, m, false)
}
func (store *Store) expireHuman(tx *gorm.DB, m *model.Message, c *humanContent, now time.Time, active bool) error {
	q := humanQuestion(*m, *c)
	if q.Expire(now) {
		c.Blocks[0].Status, c.Blocks[0].Reason = q.Status, q.Reason
		return store.saveHuman(tx, m, *c, active)
	}
	return nil
}

// +spec=`同 conv/actor/effect_key 同输入复用 Message 和 deadline；并行问题在同一事务锁下独立收口`
func (store *Store) CreateHuman(ctx context.Context, r contract.HumanRequest) (result contract.HumanResult, err error) {
	if e := r.Validate(); e != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidHuman, e)
	}

	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(sum[:])
	err = store.withHumanContext(ctx, r, func(tx *gorm.DB) error {
		var existing []model.Message
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id = ? AND kind = ? AND actor_key = ? AND purpose = ?", r.ConversationID, r.Actor.Kind, r.Actor.Key, "human_request")
		if err := query.Find(&existing).Error; err != nil {
			return err
		}
		if err := hydrateMessages(tx.Clauses(clause.Locking{Strength: "UPDATE"}), existing); err != nil {
			return err
		}
		for _, m := range existing {
			c, err := decodeHuman(m)
			if err != nil {
				return err
			}
			if c.Meta.Human.EffectKey != r.EffectKey {
				continue
			}
			if c.Meta.Human.Fingerprint != fingerprint {
				return ErrConflict
			}
			if err := store.expireHuman(tx, &m, &c, time.Now().UTC(), true); err != nil {
				return err
			}
			result, err = humanResult(tx, m, c)
			return err
		}
		now := time.Now().UTC()
		question := domain.NewHumanQuestion(r, now)
		deadline := question.Deadline
		c := humanContent{Version: "1.1", Biz: "chat", Blocks: []contract.HumanBlock{{ID: "human", Type: r.Type, Title: r.Title, Prompt: r.Prompt, Choices: r.Choices, AllowOther: r.AllowOther, ConfirmLabel: r.ConfirmLabel, DeclineLabel: r.DeclineLabel, Status: question.Status, Deadline: deadline}}}
		c.Meta.Human.EffectKey = r.EffectKey
		c.Meta.Human.Timeout = r.Timeout
		c.Meta.Human.Fingerprint = fingerprint
		content, err := json.Marshal(c)
		if err != nil {
			return err
		}
		m := model.Message{ID: uuid.V7(), ConversationID: r.ConversationID, Kind: r.Actor.Kind, ActorKey: r.Actor.Key, TargetKind: r.Target.Kind, TargetKey: r.Target.Key, ReplyToID: r.ReplyToID, Purpose: "human_request", Revision: 1, HumanDueAt: &deadline, Content: content}
		if err := store.saveMessage(tx, &m, true); err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		return err
	})
	return
}

func (store *Store) GetHuman(ctx context.Context, id string) (result contract.HumanResult, err error) {
	err = store.withHumanMessage(ctx, id, func(tx *gorm.DB, locked model.Message) error {
		m := locked
		c, err := decodeHuman(m)
		if err != nil {
			return err
		}
		if err := store.expireHuman(tx, &m, &c, time.Now().UTC(), true); err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		return err
	})
	return
}

// +spec=`答复只依 reply_to_id；deadline 与答复竞争时只有一个终态`
func (store *Store) ReplyHuman(ctx context.Context, conversationID, actor string, r contract.HumanReply) (result contract.HumanResult, err error) {
	rejected := false
	err = store.withHumanMessage(ctx, r.ReplyToID, func(tx *gorm.DB, m model.Message) error {
		if m.ConversationID != conversationID {
			return ErrNotFound
		}
		if actor == "" || m.TargetKind != contract.ActorKindUser || actor != m.TargetKey {
			return ErrForbidden
		}
		c, err := decodeHuman(m)
		if err != nil {
			return err
		}
		if err := humanRequest(m, c).ValidateReply(r); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHuman, err)
		}
		if err := store.expireHuman(tx, &m, &c, time.Now().UTC(), true); err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		if err != nil {
			return err
		}
		var previous *domain.HumanAnswer
		if result.Reply != nil {
			previous = &domain.HumanAnswer{Actor: result.Reply.Key, Outcome: result.Status, Value: result.Value}
		}
		question := humanQuestion(m, c)
		changed, resolveErr := question.Resolve(r, actor, previous)
		if errors.Is(resolveErr, domain.ErrHumanConflict) {
			// Persist an observed timeout even when the late reply is rejected.
			rejected = true
			return nil
		}
		if resolveErr != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHuman, resolveErr)
		}
		if !changed {
			return nil
		}
		// Freeze display data from the validated question, not from the client.
		// Both messages retain the accepted choice in this same transaction, so
		// rendering either page never needs a reverse lookup or a parent fetch.
		c.Blocks[0].Status, c.Blocks[0].Reason = question.Status, question.Reason
		if question.Status == contract.HumanSuccess {
			c.Blocks[0].SelectedValue = &r.Value
		}
		content, _ := json.Marshal(struct {
			Version string                     `json:"version"`
			Biz     string                     `json:"biz"`
			Meta    map[string]any             `json:"meta"`
			Blocks  []contract.HumanReplyBlock `json:"blocks"`
		}{"1.1", "chat", map[string]any{}, []contract.HumanReplyBlock{{ID: "human", Type: "human_reply", Outcome: r.Outcome, Value: r.Value, Question: c.Blocks[0]}}})
		reply := model.Message{ID: uuid.V7(), ConversationID: conversationID, Kind: contract.ActorKindUser, ActorKey: actor, TargetKind: m.Kind, TargetKey: m.ActorKey, DispatchPending: true, ReplyToID: m.ID, Purpose: "human_reply", Revision: 1, Content: content}
		var parent model.Conversation
		if err := tx.First(&parent, "id = ?", conversationID).Error; err != nil {
			return err
		}
		if err := ensureParticipantConversation(tx, parent, contract.ActorRef{Kind: reply.TargetKind, Key: reply.TargetKey}); err != nil {
			return err
		}
		if err := store.saveMessage(tx, &reply, true); err != nil {
			return err
		}
		if err := store.saveHuman(tx, &m, c, true); err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		return err
	})
	if err == nil && rejected {
		err = ErrConflict
	}
	return
}

func (store *Store) HumanMaintenance(ctx context.Context) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	err := store.db.WithContext(ctx).Where("human_due_at <= ? OR wake_pending = ?", time.Now().UTC(), true).Order("id ASC").Find(&rows).Error
	return rows, err
}
func (store *Store) AcknowledgeHumanWake(ctx context.Context, id string, revision uint64) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.db.WithContext(ctx).Model(&model.Message{}).Where("id = ? AND revision = ?", id, revision).Update("wake_pending", false).Error
}

func humanRequest(m model.Message, c humanContent) contract.HumanRequest {
	b := c.Blocks[0]
	return contract.HumanRequest{EffectKey: c.Meta.Human.EffectKey, Timeout: c.Meta.Human.Timeout, Type: b.Type, Title: b.Title, Prompt: b.Prompt, Choices: b.Choices, AllowOther: b.AllowOther, ConfirmLabel: b.ConfirmLabel, DeclineLabel: b.DeclineLabel}
}

func humanQuestion(m model.Message, c humanContent) domain.HumanQuestion {
	b := c.Blocks[0]
	return domain.HumanQuestion{Request: humanRequest(m, c), Status: b.Status, Deadline: b.Deadline, Reason: b.Reason}
}
