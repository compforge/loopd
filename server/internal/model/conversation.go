package model

import "time"

type Conversation struct {
	ID              string `gorm:"primaryKey;size:36"`
	Name            string
	ParentMessageID *string `gorm:"size:36;uniqueIndex"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Conversation) TableName() string { return "conversations" }
