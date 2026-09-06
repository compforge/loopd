package model

// MessagePart is a physical container for complete AgentUE blocks. Its ID is
// opaque to AgentUE and is always resolved within its owning Message.
type MessagePart struct {
	ID        string `gorm:"primaryKey;size:36"`
	MessageID string `gorm:"size:36;not null;index:idx_message_part_owner"`
	Content   []byte `gorm:"type:json;not null"`
	SizeBytes int    `gorm:"not null"`
}

func (MessagePart) TableName() string { return "message_parts" }
