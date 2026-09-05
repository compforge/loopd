package model

import "time"

type Conversation struct {
	ID        string `gorm:"primaryKey;size:36"`
	Name      string
	ActorKind string  `gorm:"size:16;not null;default:user"`
	ActorKey  string  `gorm:"size:128;not null;default:''"`
	TaskID    *string `gorm:"size:36;uniqueIndex"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Conversation) TableName() string { return "conversations" }
