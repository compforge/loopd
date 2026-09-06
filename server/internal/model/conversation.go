package model

import (
	"time"

	"github.com/compforge/loopd/pkg/contract"
)

type Conversation struct {
	ID        string `gorm:"primaryKey;size:36"`
	Name      string
	ActorKind contract.ActorKind `gorm:"size:128;not null;default:user;uniqueIndex:idx_conversation_actor"`
	ActorKey  string             `gorm:"size:128;not null;default:'';uniqueIndex:idx_conversation_actor"`
	ParentID  *string            `gorm:"size:36;index;uniqueIndex:idx_conversation_actor"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Conversation) TableName() string { return "conversations" }
