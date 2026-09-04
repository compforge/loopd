package model

import "time"

type Operator struct {
	ID          string `gorm:"primaryKey;size:36"`
	Key         string `gorm:"size:128;not null;uniqueIndex"`
	DisplayName string
	Description string
	ExpiresAt   time.Time `gorm:"not null;index"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Operator) TableName() string { return "operators" }
