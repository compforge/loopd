package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	loopd "github.com/compforge/loopd"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidHuman = errors.New("invalid Human request")
var ErrForbidden = errors.New("responder cannot answer this task")

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
	Blocks []loopd.HumanBlock `json:"blocks"`
}
type replyBlock struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Outcome loopd.HumanStatus `json:"outcome"`
	Value   string            `json:"value,omitempty"`
}

// TaskPair accepts explicit identities, or an unambiguous legacy two-message pair.
// It never picks a last/nearest message when multiple actors speak in parallel.
func TaskPair(rows []model.Message) (input, response model.Message, err error) {
	for _, row := range rows {
		switch row.Purpose {
		case "input":
			if input.ID != "" {
				return input, response, ErrConflict
			}
			input = row
		case "response":
			if response.ID != "" {
				return input, response, ErrConflict
			}
			response = row
		}
	}
	if input.ID == "" && response.ID == "" && len(rows) == 2 && rows[0].Purpose == "" && rows[1].Purpose == "" {
		for _, row := range rows {
			if row.Kind == "user" {
				input = row
			} else {
				response = row
			}
		}
	}
	if input.ID == "" || response.ID == "" || input.Kind != "user" || response.Kind == "user" || input.TaskID != response.TaskID || input.ConversationID != response.ConversationID {
		return input, response, ErrNotFound
	}
	return input, response, nil
}

// withHumanTask uses the original response as the lock shared by all Human
// transitions and task completion. No external call runs inside the transaction.
func (store *Store) withHumanTask(ctx context.Context, taskID string, fn func(*gorm.DB, model.Message, model.Message) error) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	rows, err := store.ListRootMessagesByTask(ctx, taskID)
	if err != nil {
		return err
	}
	input, response, err := TaskPair(rows)
	if err != nil {
		return err
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&response, "id = ?", response.ID).Error; err != nil {
			return err
		}
		if input.Purpose == "" {
			if err := tx.Model(&model.Message{}).Where("id = ?", input.ID).Update("purpose", "input").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Message{}).Where("id = ?", response.ID).Updates(map[string]any{"purpose": "response", "reply_to_message_id": input.ID}).Error; err != nil {
				return err
			}
		}
		return fn(tx, input, response)
	})
	return mapError(err)
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

func humanResult(tx *gorm.DB, m model.Message, c humanContent) (loopd.HumanResult, error) {
	b := c.Blocks[0]
	result := loopd.HumanResult{Message: publicMessage(m), Status: b.Status, Deadline: b.Deadline, Reason: b.Reason}
	if b.Status == loopd.HumanSuccess || b.Status == loopd.HumanDismissed {
		var reply model.Message
		if err := tx.First(&reply, "reply_to_message_id = ? AND purpose = ?", m.ID, "human_reply").Error; err != nil {
			return result, err
		}
		var content struct {
			Blocks []replyBlock `json:"blocks"`
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
func publicMessage(m model.Message) loopd.Message {
	return loopd.Message{ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, Kind: loopd.Role(m.Kind), Key: m.ActorKey, Content: m.Content, ReplyToMessageID: m.ReplyToMessageID, Purpose: m.Purpose, Revision: m.Revision, Timestamped: loopd.Timestamped{CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}}
}
func saveHuman(tx *gorm.DB, m *model.Message, c humanContent, wake bool) error {
	content, err := json.Marshal(c)
	if err != nil {
		return err
	}
	m.Content = content
	m.Revision++
	m.HumanDueAt = nil
	m.WakePending = wake
	return tx.Save(m).Error
}
func expireHuman(tx *gorm.DB, m *model.Message, c *humanContent, now time.Time, active bool) error {
	if c.Blocks[0].Status == loopd.HumanPending && !now.Before(c.Blocks[0].Deadline) {
		c.Blocks[0].Status = loopd.HumanTimeout
		return saveHuman(tx, m, *c, active)
	}
	return nil
}

// +spec=`同 task/effect_key 同输入复用 Message 和 deadline；并行问题在同一事务锁下独立收口`
func (store *Store) CreateHuman(ctx context.Context, r loopd.HumanRequest, active bool) (result loopd.HumanResult, err error) {
	if e := r.Validate(); e != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidHuman, e)
	}
	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(sum[:])
	err = store.withHumanTask(ctx, r.TaskID, func(tx *gorm.DB, input, response model.Message) error {
		var existing []model.Message
		if err := tx.Where("task_id = ? AND purpose = ?", r.TaskID, "human_request").Find(&existing).Error; err != nil {
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
			if err := expireHuman(tx, &m, &c, time.Now().UTC(), response.DeliveryState == ""); err != nil {
				return err
			}
			result, err = humanResult(tx, m, c)
			return err
		}
		if !active || response.DeliveryState != "" || response.Kind != "operator" {
			return ErrConflict
		}
		now := time.Now().UTC()
		deadline := now.Add(r.Timeout)
		c := humanContent{Version: "1.0", Biz: "chat", Blocks: []loopd.HumanBlock{{ID: "human", Type: r.Type, Title: r.Title, Prompt: r.Prompt, Choices: r.Choices, AllowOther: r.AllowOther, ConfirmLabel: r.ConfirmLabel, DeclineLabel: r.DeclineLabel, Status: loopd.HumanPending, Deadline: deadline}}}
		c.Meta.Human.EffectKey = r.EffectKey
		c.Meta.Human.Timeout = r.Timeout
		c.Meta.Human.Fingerprint = fingerprint
		content, err := json.Marshal(c)
		if err != nil {
			return err
		}
		m := model.Message{ID: uuid.V7(), ConversationID: input.ConversationID, TaskID: r.TaskID, Kind: "operator", ActorKey: response.ActorKey, ReplyToMessageID: input.ID, Purpose: "human_request", Revision: 1, HumanDueAt: &deadline, Content: content}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		return err
	})
	return
}

func (store *Store) GetHuman(ctx context.Context, id string) (result loopd.HumanResult, err error) {
	m, err := store.GetMessage(ctx, id)
	if err != nil {
		return result, err
	}
	err = store.withHumanTask(ctx, m.TaskID, func(tx *gorm.DB, input, response model.Message) error {
		if err := tx.First(&m, "id = ?", id).Error; err != nil {
			return err
		}
		c, err := decodeHuman(m)
		if err != nil {
			return err
		}
		if err := expireHuman(tx, &m, &c, time.Now().UTC(), response.DeliveryState == ""); err != nil {
			return err
		}
		result, err = humanResult(tx, m, c)
		return err
	})
	return
}

// +spec=`答复只依 reply_to_message_id；deadline、答复和 Complete 竞争时只有一个终态`
func (store *Store) ReplyHuman(ctx context.Context, conversationID, taskID, actor string, r loopd.HumanReply) (result loopd.HumanResult, err error) {
	rejected := false
	err = store.withHumanTask(ctx, taskID, func(tx *gorm.DB, input, response model.Message) error {
		if input.ConversationID != conversationID {
			return ErrNotFound
		}
		if actor == "" || actor != input.ActorKey {
			return ErrForbidden
		}
		var m model.Message
		if err := tx.First(&m, "id = ? AND task_id = ? AND conversation_id = ?", r.ReplyToMessageID, taskID, conversationID).Error; err != nil {
			return err
		}
		c, err := decodeHuman(m)
		if err != nil {
			return err
		}
		if err := humanRequest(m, c).ValidateReply(r); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidHuman, err)
		}
		if err := expireHuman(tx, &m, &c, time.Now().UTC(), response.DeliveryState == ""); err != nil {
			return err
		}
		if c.Blocks[0].Status != loopd.HumanPending {
			result, err = humanResult(tx, m, c)
			if err != nil {
				return err
			}
			// Committed timeout must survive a rejected late reply.
			rejected = result.Reply == nil || result.Reply.Key != actor || result.Status != r.Outcome || result.Value != r.Value
			return nil
		}
		if response.DeliveryState != "" {
			return ErrConflict
		}
		content, _ := json.Marshal(struct {
			Version string         `json:"version"`
			Biz     string         `json:"biz"`
			Meta    map[string]any `json:"meta"`
			Blocks  []replyBlock   `json:"blocks"`
		}{"1.0", "chat", map[string]any{}, []replyBlock{{ID: "human", Type: "human_reply", Outcome: r.Outcome, Value: r.Value}}})
		reply := model.Message{ID: uuid.V7(), ConversationID: conversationID, TaskID: taskID, Kind: "user", ActorKey: actor, ReplyToMessageID: m.ID, Purpose: "human_reply", Revision: 1, Content: content}
		if err := tx.Create(&reply).Error; err != nil {
			return err
		}
		c.Blocks[0].Status = r.Outcome
		if err := saveHuman(tx, &m, c, true); err != nil {
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

// BeginCompletion closes the create/reply gate durably before external delivery.
// completion is persisted so Run can retry after process death or delete failure.
func (store *Store) BeginCompletion(ctx context.Context, taskID string, completion []byte, failed bool) error {
	blocked := false
	err := store.withHumanTask(ctx, taskID, func(tx *gorm.DB, input, response model.Message) error {
		if response.DeliveryState != "" {
			if string(response.Completion) != string(completion) {
				return ErrConflict
			}
			return nil
		}
		var pending []model.Message
		if err := tx.Where("task_id = ? AND purpose = ? AND human_due_at IS NOT NULL", taskID, "human_request").Find(&pending).Error; err != nil {
			return err
		}
		for i := range pending {
			m := &pending[i]
			c, err := decodeHuman(*m)
			if err != nil {
				return err
			}
			if err := expireHuman(tx, m, &c, time.Now().UTC(), true); err != nil {
				return err
			}
			if c.Blocks[0].Status != loopd.HumanPending {
				continue
			}
			if !failed {
				blocked = true
				continue
			}
			c.Blocks[0].Status = loopd.HumanFailure
			c.Blocks[0].Reason = "task_ended"
			if err := saveHuman(tx, m, c, false); err != nil {
				return err
			}
		}
		if blocked {
			return nil
		}
		if err := tx.Model(&model.Message{}).Where("task_id = ? AND wake_pending = ?", taskID, true).Update("wake_pending", false).Error; err != nil {
			return err
		}
		return tx.Model(&response).Updates(map[string]any{"delivery_state": "closing", "completion": completion}).Error
	})
	if err == nil && blocked {
		return ErrConflict
	}
	return err
}
func (store *Store) FinishCompletion(ctx context.Context, taskID string) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.db.WithContext(ctx).Model(&model.Message{}).Where("task_id = ? AND purpose = ?", taskID, "response").Update("delivery_state", "closed").Error
}
func (store *Store) HumanMaintenance(ctx context.Context) ([]model.Message, error) {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	var rows []model.Message
	err := store.db.WithContext(ctx).Where("human_due_at <= ? OR wake_pending = ? OR delivery_state = ?", time.Now().UTC(), true, "closing").Order("id ASC").Find(&rows).Error
	return rows, err
}
func (store *Store) AcknowledgeHumanWake(ctx context.Context, id string, revision uint64) error {
	ctx, cancel := store.withTimeout(ctx)
	defer cancel()
	return store.db.WithContext(ctx).Model(&model.Message{}).Where("id = ? AND revision = ?", id, revision).Update("wake_pending", false).Error
}

func humanRequest(m model.Message, c humanContent) loopd.HumanRequest {
	b := c.Blocks[0]
	return loopd.HumanRequest{TaskID: m.TaskID, EffectKey: c.Meta.Human.EffectKey, Timeout: c.Meta.Human.Timeout, Type: b.Type, Title: b.Title, Prompt: b.Prompt, Choices: b.Choices, AllowOther: b.AllowOther, ConfirmLabel: b.ConfirmLabel, DeclineLabel: b.DeclineLabel}
}
