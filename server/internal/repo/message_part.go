package repo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/compforge/agentue/sdks/go/storage"
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
			if err := c.materialize(id); err != nil {
				return err
			}
			raw, err := json.Marshal(blocks[i])
			if err != nil {
				return err
			}
			// A page budget may stop between blocks, never reject its first block.
			if len(result.Data) >= 100 || (len(result.Data) > 0 && total+len(raw) > maxBlockPageBytes) {
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
	defaultMessagePartBytes    = 64 << 10
	maxStoredContentBytes      = 64 << 10
)

type partContent struct {
	Blocks []map[string]any `json:"blocks"`
}
type contentPart struct {
	row   model.MessagePart
	body  partContent
	dirty bool
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

// A reference addresses a group: one ordinary Part or multiple frame Parts.
// Group membership and block ordering are storage concerns, not UI events.
func decodePart(rows ...model.MessagePart) (*contentPart, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("missing stored part group")
	}
	p := &contentPart{row: rows[0]}
	if p.row.GroupID == "" {
		return nil, fmt.Errorf("part %s has no group", p.row.ID)
	}
	p.row.ID = p.row.GroupID
	var frames []storage.FrameBlock
	ids := map[string]bool{}
	for _, row := range rows {
		if row.MessageID != p.row.MessageID || row.GroupID != p.row.GroupID {
			return nil, fmt.Errorf("mixed part ownership in group %s", p.row.GroupID)
		}
		var body partContent
		if err := decodeContentJSON(row.Content, &body); err != nil {
			return nil, err
		}
		for _, block := range body.Blocks {
			if err := ui.ValidateBlock(block); err != nil {
				return nil, err
			}
			if _, ref := block["ref"]; ref {
				return nil, fmt.Errorf("nested reference in part %s", row.ID)
			}
			id := block["id"].(string)
			if ids[id] {
				return nil, fmt.Errorf("duplicate block %s in group %s", id, row.GroupID)
			}
			ids[id] = true
			if block["type"] == storage.FrameType {
				raw, err := json.Marshal(block)
				if err != nil {
					return nil, err
				}
				var frame storage.FrameBlock
				if err := json.Unmarshal(raw, &frame); err != nil {
					return nil, err
				}
				frames = append(frames, frame)
			} else {
				p.body.Blocks = append(p.body.Blocks, block)
			}
		}
	}
	if len(frames) > 0 {
		if len(p.body.Blocks) != 0 {
			return nil, fmt.Errorf("mixed frames and complete blocks in group %s", p.row.GroupID)
		}
		raw, err := storage.Unframe(frames)
		if err != nil {
			return nil, fmt.Errorf("part group %s: %w", p.row.GroupID, err)
		}
		var block map[string]any
		if err := decodeContentJSON(raw, &block); err != nil {
			return nil, err
		}
		p.body.Blocks = []map[string]any{block}
	}
	return p, nil
}

func (c *messageContent) loadPart(key string) (*contentPart, error) {
	if p := c.parts[key]; p != nil {
		return p, nil
	}
	var rows []model.MessagePart
	// Writers already hold the owning Message lock. Readers use their repeatable
	// snapshot; no FOR UPDATE is needed (or valid in read-only transactions).
	if err := c.tx.Where("group_id = ? AND message_id = ?", key, c.owner).Find(&rows).Error; err != nil {
		return nil, err
	}
	p, err := decodePart(rows...)
	if err != nil {
		return nil, fmt.Errorf("message %s part group %s: %w", c.owner, key, err)
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
	p := &contentPart{row: model.MessagePart{ID: id, MessageID: c.owner, GroupID: id}, body: partContent{Blocks: []map[string]any{block}}, dirty: true}
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
			// A large block owns one logical group; persist splits it into bounded
			// physical frame Parts without changing the block identity.
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
	stored, err := json.Marshal(c.snapshot)
	// The root limit includes metadata and the reference directory, not just
	// inline block bytes. Externalize remaining inline suffixes when necessary.
	for i := len(c.snapshot["blocks"].([]any)) - 1; err == nil && len(stored) > maxStoredContentBytes && i >= 0; i-- {
		blocks := c.snapshot["blocks"].([]any)
		block := blocks[i].(map[string]any)
		if _, ref := block["ref"]; ref {
			continue
		}
		ref, placeErr := c.place(block, s.messagePartBytes)
		if placeErr != nil {
			return nil, placeErr
		}
		blocks[i] = map[string]any{"id": block["id"], "ref": ref}
		c.snapshot["version"] = ui.ProtocolVersion
		stored, err = json.Marshal(c.snapshot)
	}
	if err == nil && len(stored) > maxStoredContentBytes {
		return nil, fmt.Errorf("%w: metadata and block references must fit within 64 KiB", ErrContentTooLarge)
	}
	return stored, err
}

// +spec=`Every stored content column is bounded; frame replacement and revision updates commit in the owning Message transaction.`
func (c *messageContent) persist(maxBytes int) error {
	maxBytes = min(maxBytes, maxStoredContentBytes)
	for _, p := range c.parts {
		if !p.dirty {
			continue
		}
		data, err := json.Marshal(p.body)
		if err != nil {
			return err
		}
		payloads := [][]byte{data}
		if len(data) > maxBytes {
			if len(p.body.Blocks) != 1 {
				return fmt.Errorf("%w: oversized multi-block part group", ErrContentTooLarge)
			}
			block, err := json.Marshal(p.body.Blocks[0])
			if err != nil {
				return err
			}
			// Frame sizes include their JSON envelopes; reserve the Part wrapper.
			frames, err := storage.Frame(block, maxBytes-len(`{"blocks":[]}`))
			if err != nil {
				return fmt.Errorf("%w: %v", ErrContentTooLarge, err)
			}
			payloads = make([][]byte, 0, len(frames))
			for _, frame := range frames {
				body, err := json.Marshal(struct {
					Blocks []storage.FrameBlock `json:"blocks"`
				}{Blocks: []storage.FrameBlock{frame}})
				if err != nil {
					return err
				}
				payloads = append(payloads, body)
			}
		}
		// Replace the complete group atomically; never mix frame revisions.
		if err := c.tx.Where("message_id = ? AND group_id = ?", c.owner, p.row.GroupID).Delete(&model.MessagePart{}).Error; err != nil {
			return err
		}
		for i, content := range payloads {
			if len(content) > maxBytes {
				return ErrContentTooLarge
			}
			id := p.row.GroupID
			if i > 0 {
				id = uuid.V7()
			}
			row := model.MessagePart{ID: id, MessageID: c.owner, GroupID: p.row.GroupID, Content: content, SizeBytes: len(content)}
			if err := c.tx.Create(&row).Error; err != nil {
				return err
			}
		}
		p.dirty = false
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
	for _, value := range incoming.snapshot["blocks"].([]any) {
		if value.(map[string]any)["type"] == storage.FrameType {
			return fmt.Errorf("%w: storage frames are not logical input blocks", ErrInvalidContent)
		}
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
	if len(incoming.parts) == 0 && len(incoming.originalRefs) == 0 && len(full) <= maxStoredContentBytes {
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
	if err := incoming.persist(s.messagePartBytes); err != nil {
		return err
	}
	q := tx.Where("message_id = ?", m.ID)
	if len(keys) > 0 {
		q = q.Where("group_id NOT IN ?", keys)
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

// +spec=`同一次读取的 Message 与 Parts 属于同一数据库快照；引用按 message_id + group_id + block_id 解析`
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
	groupRows := map[string][]model.MessagePart{}
	for _, row := range rows {
		groupRows[row.GroupID] = append(groupRows[row.GroupID], row)
	}
	parts := map[string]*contentPart{}
	for groupID, rows := range groupRows {
		p, err := decodePart(rows...)
		if err != nil {
			return err
		}
		parts[groupID] = p
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
