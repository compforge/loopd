package loopd

// ChatContext is the read projection of messages sharing a UI chat delivery ID.
// It does not define an Operator's business task or require a tasks table.
type ChatContext struct {
	Target        ActorRef     `json:"target"`
	DeliveryState string       `json:"delivery_state,omitempty"`
	ID            string       `json:"id"`
	Conversation  Conversation `json:"conversation"`
	// Input and Response identify the initial question and main answer, not the latest turn.
	Input                Message   `json:"input"`
	Response             Message   `json:"response"`
	History              []Message `json:"history"`
	HistoryFromMessageID string    `json:"history_from_message_id,omitempty"`
	HasEarlier           bool      `json:"has_earlier"`
}
