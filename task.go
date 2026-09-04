package loopd

// TaskContext is the loop-server view resolved from the visible messages that
// share a Task CRD identity. It is computed on read and is not stored in a
// separate tasks table.
type TaskContext struct {
	ID                   string       `json:"id"`
	Conversation         Conversation `json:"conversation"`
	Input                Message      `json:"input"`
	Response             Message      `json:"response"`
	History              []Message    `json:"history"`
	HistoryFromMessageID string       `json:"history_from_message_id,omitempty"`
	HasEarlier           bool         `json:"has_earlier"`
}
