package model

// MessagePart is a bounded physical container of blocks or storage frames.
// A stored block reference addresses GroupID within its owning Message.
// +why=`Part is only a storage optimization, not a collaboration object or public addressing boundary.`
// +rule=`Part IDs and references stay in repo/model; services, runtime and clients use logical Message content.`
type MessagePart struct {
	ID        string `gorm:"primaryKey;size:36"`
	MessageID string `gorm:"size:36;not null;index:idx_message_part_owner"`
	GroupID   string `gorm:"size:36;not null;index:idx_message_part_group"`
	Content   []byte `gorm:"type:json;not null"`
	SizeBytes int    `gorm:"not null"`
}

func (MessagePart) TableName() string { return "message_parts" }
