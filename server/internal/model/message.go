package model

import "time"

type Message struct {
	// Empty recipient kind and key explicitly address the conversation.
	TargetKind      string     `gorm:"size:128"`
	TargetKey       string     `gorm:"size:128"`
	DispatchPending bool       `gorm:"not null;default:false;index"`
	OutputKey       *string    `gorm:"size:128;uniqueIndex:idx_message_output"`
	ReplyToID       string     `gorm:"size:36;not null;default:'';index"`
	Purpose         string     `gorm:"size:24;not null;default:'';index"`
	Revision        uint64     `gorm:"not null;default:1"`
	HumanDueAt      *time.Time `gorm:"index"`
	WakePending     bool       `gorm:"not null;default:false;index"`

	ID             string `gorm:"primaryKey;size:36"`
	ConversationID string `gorm:"size:36;not null;index:idx_message_conversation"`
	TaskID         string `gorm:"size:36;not null;index:idx_message_task"`
	Kind           string `gorm:"size:128;not null"`
	ActorKey       string `gorm:"size:128;not null"`
	Content        []byte `gorm:"type:json;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Message) TableName() string { return "messages" }
