package model

import "time"

type Message struct {
	ID             string `gorm:"primaryKey;size:36"`
	ConversationID string `gorm:"size:36;not null;index:idx_message_conversation"`
	TaskID         string `gorm:"size:36;not null;index:idx_message_task"`
	Kind           string `gorm:"size:16;not null"`
	SenderKey      string `gorm:"size:128;not null"`
	Content        []byte `gorm:"type:json;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Message) TableName() string { return "messages" }
