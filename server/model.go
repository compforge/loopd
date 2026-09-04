package server

import "time"

type conversationPO struct {
	ID           string `gorm:"primaryKey;size:64"`
	Title        string
	LastSequence int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type messagePO struct {
	ID             string `gorm:"primaryKey;size:64"`
	ConversationID string `gorm:"size:64;not null;uniqueIndex:idx_message_sequence"`
	Sequence       int64  `gorm:"not null;uniqueIndex:idx_message_sequence"`
	Role           string `gorm:"size:16;not null"`
	AuthorID       string `gorm:"size:128;not null"`
	Content        string `gorm:"not null"`
	CreatedAt      time.Time
}

type invocationPO struct {
	ID                 string `gorm:"primaryKey;size:64"`
	ConversationID     string `gorm:"size:64;not null;index"`
	InputMessageID     string `gorm:"size:64;not null;uniqueIndex"`
	OutputMessageID    string `gorm:"size:64"`
	ResponderRole      string `gorm:"size:16;not null;index:idx_invocation_responder"`
	ResponderID        string `gorm:"size:128;not null;index:idx_invocation_responder"`
	ContextThroughSeq  int64
	Phase              string `gorm:"size:32;not null;index"`
	ResourceAPIVersion string
	ResourceKind       string
	ResourceNamespace  string
	ResourceName       string
	ResourceUID        string `gorm:"size:128"`
	Error              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type activityPO struct {
	ID           string `gorm:"primaryKey;size:64"`
	InvocationID string `gorm:"size:64;not null;uniqueIndex:idx_activity_key"`
	Key          string `gorm:"size:128;not null;uniqueIndex:idx_activity_key"`
	ParentID     string `gorm:"size:64"`
	ActorRole    string `gorm:"size:16;not null"`
	ActorID      string `gorm:"size:128;not null"`
	Kind         string `gorm:"size:64;not null"`
	Title        string
	Detail       string
	Phase        string `gorm:"size:32;not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type harnessCallPO struct {
	ID              string `gorm:"primaryKey;size:64"`
	InvocationID    string `gorm:"size:64;not null;index"`
	OwnerUID        string `gorm:"size:128;not null;uniqueIndex:idx_call_effect"`
	EffectKey       string `gorm:"size:128;not null;uniqueIndex:idx_call_effect"`
	RequestHash     string `gorm:"size:64;not null"`
	Target          string `gorm:"size:128;not null"`
	Prompt          string `gorm:"not null"`
	ToolsJSON       []byte
	Phase           string `gorm:"size:32;not null;index"`
	ExternalRef     string
	ProviderCursor  string
	LastEventCursor uint64
	StreamText      string
	Result          string
	Error           string
	LastActivityAt  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type interactionPO struct {
	ID            string `gorm:"primaryKey;size:64"`
	InvocationID  string `gorm:"size:64;not null;index"`
	OwnerUID      string `gorm:"size:128;not null;uniqueIndex:idx_interaction_effect"`
	EffectKey     string `gorm:"size:128;not null;uniqueIndex:idx_interaction_effect"`
	RequestHash   string `gorm:"size:64;not null"`
	RequesterRole string `gorm:"size:16;not null"`
	RequesterID   string `gorm:"size:128;not null"`
	Kind          string `gorm:"size:32;not null"`
	Title         string
	Prompt        string `gorm:"not null"`
	OptionsJSON   []byte
	Phase         string `gorm:"size:32;not null;index"`
	Answer        string
	ExpiresAt     *time.Time
	ResolvedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type invocationEventPO struct {
	Cursor         uint64 `gorm:"primaryKey;autoIncrement"`
	InvocationID   string `gorm:"size:64;not null;index:idx_invocation_cursor"`
	CallID         string `gorm:"size:64;index:idx_call_cursor"`
	ProviderCursor string `gorm:"size:256;index:idx_provider_cursor"`
	Kind           string `gorm:"size:64;not null;index"`
	Data           []byte
	CreatedAt      time.Time
}

func (conversationPO) TableName() string    { return "conversations" }
func (messagePO) TableName() string         { return "messages" }
func (invocationPO) TableName() string      { return "invocations" }
func (activityPO) TableName() string        { return "activities" }
func (harnessCallPO) TableName() string     { return "harness_calls" }
func (interactionPO) TableName() string     { return "interactions" }
func (invocationEventPO) TableName() string { return "invocation_events" }
