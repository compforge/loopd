package repo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	ui "github.com/compforge/agentue/sdks/go/ui"
	"github.com/compforge/loopd/pkg/contract"
	"github.com/compforge/loopd/server/internal/model"
	"github.com/qiankunli/go-stdx/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxBlockPageBytes = 1 << 20

// MessageBlocks reads logical blocks at one revision, never exposing physical refs.
// The cursor is a revision plus logical position, not a Part identity.
// +spec=`Logical content and pagination are independent of physical Part layout; revision changes cannot silently mix pages.`
func (s *Store) MessageBlocks(ctx context.Context, convID, id, blockID, cursor string) (contract.BlockPage, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	result := contract.BlockPage{Data: []json.RawMessage{}}
	position := 0
	var revision uint64
	if cursor != "" {
		a, b, ok := strings.Cut(cursor, ":")
		var err error
		revision, err = strconv.ParseUint(a, 10, 64)
		if err != nil || !ok {
			return result, ErrInvalidContent
		}
		position, err = strconv.Atoi(b)
		if err != nil || position < 0 {
			return result, ErrInvalidContent
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m model.Message
		if err := tx.Where("conversation_id = ? AND id = ?", convID, id).First(&m).Error; err != nil {
			return err
		}
		result.Revision = m.Revision
		if cursor != "" && revision != m.Revision {
			return ErrConflict
		}
		c, err := openMessageContent(tx, m)
		if err != nil {
			return err
		}
		blocks := c.snapshot["blocks"].([]any)
		if blockID != "" {
			var ok bool
			position, ok = c.positions[blockID]
			if !ok {
				return ErrNotFound
			}
		}
		if position > len(blocks) {
			return ErrInvalidContent
		}
		total := 0
		for i := position; i < len(blocks); i++ {
			block := blocks[i].(map[string]any)
			id := block["id"].(string)
			if ref, ok := block["ref"].(string); ok {
				if _, loaded := c.parts[ref]; !loaded {
					var row model.MessagePart
					if err := tx.Where("message_id = ? AND id = ?", m.ID, ref).First(&row).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return fmt.Errorf("message %s has missing stored content", m.ID)
						}
						return err
					}
					p, err := decodePart(row)
					if err != nil {
						return err
					}
					c.parts[ref] = p
				}
				if err := c.materialize(id); err != nil {
					return err
				}
			}
			raw, err := json.Marshal(blocks[i])
			if err != nil {
				return err
			}
			if len(raw) > maxBlockPageBytes {
				return ErrContentTooLarge
			}
			if len(result.Data) >= 100 || total+len(raw) > maxBlockPageBytes {
				result.Next = strconv.FormatUint(m.Revision, 10) + ":" + strconv.Itoa(i)
				break
			}
			result.Data = append(result.Data, raw)
			total += len(raw)
			if blockID != "" {
				break
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, mapError(err)
}

const (
	defaultMessageInlineBlocks = 32
	defaultMessageInlineBytes  = 64 << 10
	defaultMessagePartBytes    = 256 << 10
)

type partContent struct {
	Blocks []map[string]any `json:"blocks"`
}
type contentPart struct {
	row   model.MessagePart
	body  partContent
	dirty bool
	fresh bool
}

// messageContent routes updates by stable block ID, independently of where a
// block currently lives. Only loaded/dirty Parts participate in a normal patch.
type messageContent struct {
	tx           *gorm.DB
	owner        string
	snapshot     map[string]any
	positions    map[string]int
	originalRefs map[string]string
	parts        map[string]*contentPart
	tail         string
}

func openMessageContent(tx *gorm.DB, m model.Message) (*messageContent, error) {
	c := &messageContent{tx: tx, owner: m.ID, originalRefs: map[string]string{}, parts: map[string]*contentPart{}, positions: map[string]int{}}
	if err := decodeContentJSON(m.Content, &c.snapshot); err != nil {
		return nil, err
	}
	if err := ui.ValidateModel(c.snapshot); err != nil {
		return nil, fmt.Errorf("message %s content: %w", m.ID, err)
	}
	for i, value := range c.snapshot["blocks"].([]any) {
		block := value.(map[string]any)
		c.positions[block["id"].(string)] = i
		if ref, ok := block["ref"].(string); ok {
			c.originalRefs[block["id"].(string)] = ref
			c.tail = ref
		}
	}
	return c, nil
}

func decodePart(row model.MessagePart) (*contentPart, error) {
	p := &contentPart{row: row}
	if err := decodeContentJSON(row.Content, &p.body); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, b := range p.body.Blocks {
		if err := ui.ValidateBlock(b); err != nil {
			return nil, err
		}
		if _, ref := b["ref"]; ref {
			return nil, fmt.Errorf("nested reference in part %s", row.ID)
		}
		id := b["id"].(string)
		if ids[id] {
			return nil, fmt.Errorf("duplicate block %s in part %s", id, row.ID)
		}
		ids[id] = true
	}
	return p, nil
}

func (c *messageContent) loadPart(key string) (*contentPart, error) {
	if p := c.parts[key]; p != nil {
		return p, nil
	}
	var row model.MessagePart
	if err := c.tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ? AND message_id = ?", key, c.owner).Error; err != nil {
		return nil, fmt.Errorf("message %s part %s: %w", c.owner, key, err)
	}
	p, err := decodePart(row)
	if err != nil {
		return nil, fmt.Errorf("message %s part %s: %w", c.owner, key, err)
	}
	c.parts[key] = p
	return p, nil
}

func (c *messageContent) materialize(id string) error {
	index, exists := c.positions[id]
	if !exists {
		return nil
	}
	blocks := c.snapshot["blocks"].([]any)
	block := blocks[index].(map[string]any)
	ref, ok := block["ref"].(string)
	if !ok {
		return nil
	}
	p, err := c.loadPart(ref)
	if err != nil {
		return err
	}
	for _, body := range p.body.Blocks {
		if body["id"] == id {
			blocks[index] = body
			return nil
		}
	}
	return fmt.Errorf("message %s part %s does not contain block %s", c.owner, ref, id)
}

func (c *messageContent) newPart(block map[string]any) *contentPart {
	id := uuid.V7()
	p := &contentPart{row: model.MessagePart{ID: id, MessageID: c.owner}, body: partContent{Blocks: []map[string]any{block}}, dirty: true, fresh: true}
	c.parts[id] = p
	c.tail = id
	return p
}

func partBytes(blocks []map[string]any) (int, error) {
	data, err := json.Marshal(partContent{Blocks: blocks})
	return len(data), err
}

func (c *messageContent) place(block map[string]any, maxBytes int) (string, error) {
	id := block["id"].(string)
	if key := c.originalRefs[id]; key != "" {
		p, err := c.loadPart(key)
		if err != nil {
			return "", err
		}
		for i, old := range p.body.Blocks {
			if old["id"] != id {
				continue
			}
			p.body.Blocks[i] = block
			p.dirty = true
			size, err := partBytes(p.body.Blocks)
			if err != nil {
				return "", err
			}
			// A single oversized block occupies its own Part. The limit is a packing
			// target; splitting inside a block is deliberately not a protocol change.
			if size <= maxBytes || len(p.body.Blocks) == 1 {
				return key, nil
			}
			p.body.Blocks = append(p.body.Blocks[:i], p.body.Blocks[i+1:]...)
			return c.newPart(block).row.ID, nil
		}
		return "", fmt.Errorf("message %s part %s does not contain block %s", c.owner, key, id)
	}
	if c.tail != "" {
		p, err := c.loadPart(c.tail)
		if err != nil {
			return "", err
		}
		candidate := append(append([]map[string]any(nil), p.body.Blocks...), block)
		size, err := partBytes(candidate)
		if err != nil {
			return "", err
		}
		if size <= maxBytes {
			p.body.Blocks = candidate
			p.dirty = true
			return p.row.ID, nil
		}
	}
	return c.newPart(block).row.ID, nil
}

// +spec=`内联前缀超出数量或字节预算后外置；已外置 block 不搬回；更新旧 block 不改变展示顺序`
func (s *Store) packContent(c *messageContent) ([]byte, error) {
	count, bytes := 0, 0
	external := false
	for i, value := range c.snapshot["blocks"].([]any) {
		b := value.(map[string]any)
		if _, ref := b["ref"]; ref {
			external = true
			continue
		}
		data, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		id := b["id"].(string)
		if !external && c.originalRefs[id] == "" && count < s.messageInlineBlocks && bytes+len(data) <= s.messageInlineBytes {
			count++
			bytes += len(data)
			continue
		}
		external = true
		ref, err := c.place(b, s.messagePartBytes)
		if err != nil {
			return nil, err
		}
		c.snapshot["blocks"].([]any)[i] = map[string]any{"id": id, "ref": ref}
	}
	if external {
		c.snapshot["version"] = ui.ProtocolVersion
	}
	return json.Marshal(c.snapshot)
}

func (c *messageContent) persist() error {
	for _, p := range c.parts {
		if !p.dirty {
			continue
		}
		data, err := json.Marshal(p.body)
		if err != nil {
			return err
		}
		p.row.Content, p.row.SizeBytes = data, len(data)
		if p.fresh {
			err = c.tx.Create(&p.row).Error
		} else {
			err = c.tx.Model(&p.row).Updates(map[string]any{"content": data, "size_bytes": len(data)}).Error
		}
		if err != nil {
			return err
		}
		p.dirty = false
		p.fresh = false
	}
	return nil
}

// saveMessage accepts a complete model and returns that complete model, while
// persisting an inline/reference snapshot. Callers own the surrounding transaction.
func (s *Store) saveMessage(tx *gorm.DB, m *model.Message, create bool) error {
	full := m.Content
	incoming, err := openMessageContent(tx, *m)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidContent, err)
	}
	if len(incoming.originalRefs) != 0 {
		return fmt.Errorf("%w: complete block bodies are required", ErrInvalidContent)
	}
	if !create {
		var previous model.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&previous, "id = ?", m.ID).Error; err != nil {
			return err
		}
		old, err := openMessageContent(tx, previous)
		if err != nil {
			return err
		}
		incoming.originalRefs, incoming.tail = old.originalRefs, old.tail
	}
	originalVersion := incoming.snapshot["version"]
	stored, err := s.packContent(incoming)
	if err != nil {
		return err
	}
	if len(incoming.parts) == 0 && len(incoming.originalRefs) == 0 {
		stored = full
	}
	m.Content = stored
	if create {
		err = tx.Create(m).Error
	} else {
		err = tx.Save(m).Error
	}
	if err != nil {
		return err
	}
	// Whole-model replacement can remove blocks. Remove obsolete bodies from
	// retained Parts too; retained reference keys remain scoped to this message.
	keep := map[string]map[string]bool{}
	for _, value := range incoming.snapshot["blocks"].([]any) {
		b := value.(map[string]any)
		if ref, ok := b["ref"].(string); ok {
			if keep[ref] == nil {
				keep[ref] = map[string]bool{}
			}
			keep[ref][b["id"].(string)] = true
		}
	}
	keys := []string{}
	for key, ids := range keep {
		keys = append(keys, key)
		p, err := incoming.loadPart(key)
		if err != nil {
			return err
		}
		filtered := []map[string]any{}
		for _, b := range p.body.Blocks {
			if ids[b["id"].(string)] {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) != len(p.body.Blocks) {
			p.body.Blocks = filtered
			p.dirty = true
		}
	}
	if err := incoming.persist(); err != nil {
		return err
	}
	q := tx.Where("message_id = ?", m.ID)
	if len(keys) > 0 {
		q = q.Where("id NOT IN ?", keys)
	}
	if err := q.Delete(&model.MessagePart{}).Error; err != nil {
		return err
	}
	// Reuse the caller's full body; only the storage version may have advanced.
	if incoming.snapshot["version"] != originalVersion {
		var snapshot map[string]any
		if err := decodeContentJSON(full, &snapshot); err != nil {
			return err
		}
		snapshot["version"] = incoming.snapshot["version"]
		full, err = json.Marshal(snapshot)
		if err != nil {
			return err
		}
	}
	m.Content = full
	return nil
}

// +spec=`同一次读取的 Message 与 Parts 属于同一数据库快照；引用按 message_id + part_id + block_id 解析`
func hydrateMessages(tx *gorm.DB, messages []model.Message) error {
	contents := make([]*messageContent, len(messages))
	owners := []string{}
	for i, m := range messages {
		c, err := openMessageContent(tx, m)
		if err != nil {
			return err
		}
		contents[i] = c
		if len(c.originalRefs) > 0 {
			owners = append(owners, m.ID)
		}
	}
	if len(owners) == 0 {
		return nil
	}
	var rows []model.MessagePart
	if err := tx.Where("message_id IN ?", owners).Find(&rows).Error; err != nil {
		return err
	}
	parts := map[string]*contentPart{}
	for _, row := range rows {
		p, err := decodePart(row)
		if err != nil {
			return err
		}
		parts[row.ID] = p
	}
	for i, c := range contents {
		if len(c.originalRefs) == 0 {
			continue
		}
		for id, ref := range c.originalRefs {
			p := parts[ref]
			if p == nil || p.row.MessageID != c.owner {
				return fmt.Errorf("message %s missing part %s", c.owner, ref)
			}
			c.parts[ref] = p
			if err := c.materialize(id); err != nil {
				return err
			}
		}
		data, err := json.Marshal(c.snapshot)
		if err != nil {
			return err
		}
		messages[i].Content = data
	}
	return nil
}

func hydrateMessage(tx *gorm.DB, m *model.Message) error {
	rows := []model.Message{*m}
	if err := hydrateMessages(tx, rows); err != nil {
		return err
	}
	*m = rows[0]
	return nil
}

func (s *Store) readMessages(ctx context.Context, query func(*gorm.DB) *gorm.DB) ([]model.Message, error) {
	var rows []model.Message
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := query(tx).Find(&rows).Error; err != nil {
			return err
		}
		return hydrateMessages(tx, rows)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return rows, mapError(err)
}

// Preserve arbitrary JSON numeric values when moving storage without changing
// the block's semantic payload.
func decodeContentJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing JSON content")
	}
	return nil
}
