package api

import (
	"encoding/json"
	"time"
)

type InvocationPhase string

const (
	InvocationQueued       InvocationPhase = "queued"
	InvocationRunning      InvocationPhase = "running"
	InvocationWaitingInput InvocationPhase = "waiting_input"
	InvocationSucceeded    InvocationPhase = "succeeded"
	InvocationFailed       InvocationPhase = "failed"
	InvocationCancelled    InvocationPhase = "cancelled"
)

func (phase InvocationPhase) Terminal() bool {
	return phase == InvocationSucceeded || phase == InvocationFailed || phase == InvocationCancelled
}

type Conversation struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Timestamped
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Sequence       int64     `json:"sequence"`
	Role           Role      `json:"role"`
	AuthorID       string    `json:"author_id"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type Invocation struct {
	ID                string          `json:"id"`
	ConversationID    string          `json:"conversation_id"`
	InputMessageID    string          `json:"input_message_id"`
	OutputMessageID   string          `json:"output_message_id,omitempty"`
	Responder         ResponderRef    `json:"responder"`
	ContextThroughSeq int64           `json:"context_through_seq"`
	Phase             InvocationPhase `json:"phase"`
	Resource          *ResourceRef    `json:"resource,omitempty"`
	Error             string          `json:"error,omitempty"`
	Timestamped
}

type InvocationContext struct {
	Invocation     Invocation `json:"invocation"`
	Input          Message    `json:"input"`
	History        []Message  `json:"history"`
	HistoryFromSeq int64      `json:"history_from_seq,omitempty"`
	HasEarlier     bool       `json:"has_earlier"`
}

type ActivityPhase string

const (
	ActivityPending   ActivityPhase = "pending"
	ActivityRunning   ActivityPhase = "running"
	ActivitySucceeded ActivityPhase = "succeeded"
	ActivityFailed    ActivityPhase = "failed"
)

type Activity struct {
	ID           string        `json:"id"`
	InvocationID string        `json:"invocation_id"`
	Key          string        `json:"key"`
	ParentID     string        `json:"parent_id,omitempty"`
	Actor        ResponderRef  `json:"actor"`
	Kind         string        `json:"kind"`
	Title        string        `json:"title"`
	Detail       string        `json:"detail,omitempty"`
	Phase        ActivityPhase `json:"phase"`
	Timestamped
}

type InvocationEvent struct {
	Cursor       uint64          `json:"cursor"`
	InvocationID string          `json:"invocation_id"`
	CallID       string          `json:"call_id,omitempty"`
	Kind         string          `json:"kind"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    time.Time       `json:"created_at"`
}

type OperatorEvent struct {
	Event      InvocationEvent `json:"event"`
	Invocation Invocation      `json:"invocation"`
}

type CreateConversationRequest struct {
	Title string `json:"title,omitempty"`
}

type CreateMessageRequest struct {
	Content   string       `json:"content"`
	Responder ResponderRef `json:"responder"`
}

type CreateMessageResponse struct {
	Message    Message    `json:"message"`
	Invocation Invocation `json:"invocation"`
}

type AcceptInvocationRequest struct {
	Resource ResourceRef `json:"resource"`
}

type ReplyRequest struct {
	Content string `json:"content"`
}

type ActivityRequest struct {
	Key      string        `json:"key"`
	ParentID string        `json:"parent_id,omitempty"`
	Actor    ResponderRef  `json:"actor"`
	Kind     string        `json:"kind"`
	Title    string        `json:"title"`
	Detail   string        `json:"detail,omitempty"`
	Phase    ActivityPhase `json:"phase"`
}
