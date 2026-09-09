package contract

import "encoding/json"

type MessageOrder string

const (
	MessageAsc  MessageOrder = "asc"
	MessageDesc MessageOrder = "desc"
)

// MessageQuery selects identities, not content revisions.
type MessageQuery struct {
	IDs      []string
	Before   string
	After    string
	Order    MessageOrder
	Limit    int
	Statuses []MessageStatus
}

// MessageInfo is the lightweight, point-in-time description of a Message.
type MessageInfo struct {
	ID             string        `json:"id"`
	ConversationID string        `json:"conversation_id"`
	TaskID         string        `json:"task_id"`
	SourceKind     ActorKind     `json:"source_kind"`
	SourceKey      string        `json:"source_key"`
	TargetKind     ActorKind     `json:"target_kind,omitempty"`
	TargetKey      string        `json:"target_key,omitempty"`
	ReplyToID      string        `json:"reply_to_id,omitempty"`
	Status         MessageStatus `json:"status"`
	Revision       uint64        `json:"revision"`
	Timestamped
}

func (m Message) Info() MessageInfo {
	return MessageInfo{ID: m.ID, ConversationID: m.ConversationID, TaskID: m.TaskID, SourceKind: m.SourceKind, SourceKey: m.SourceKey,
		TargetKind: m.TargetKind, TargetKey: m.TargetKey, ReplyToID: m.ReplyToID,
		Status: m.Status, Revision: m.Revision, Timestamped: m.Timestamped}
}

type MessagePage struct {
	Data []MessageInfo `json:"data"`
	Next string        `json:"next,omitempty"`
}

type BlockSnapshot struct {
	Revision uint64          `json:"revision"`
	Block    json.RawMessage `json:"block"`
}

type BlockPage struct {
	Revision uint64            `json:"revision"`
	Data     []json.RawMessage `json:"data"`
	Next     string            `json:"next,omitempty"`
}
