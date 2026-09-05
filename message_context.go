package loopd

// MessageContext is a bounded history snapshot through a particular message.
// It imposes no question/answer pairing or business completion boundary.
type MessageContext struct {
	Conversation         Conversation `json:"conversation"`
	Message              Message      `json:"message"`
	History              []Message    `json:"history"`
	HistoryFromMessageID string       `json:"history_from_message_id,omitempty"`
	HasEarlier           bool         `json:"has_earlier"`
}
