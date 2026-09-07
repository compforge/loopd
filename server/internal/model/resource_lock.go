package model

import "time"

type ResourceLock struct {
	Resource  string    `gorm:"primaryKey;size:191"`
	LockerID  string    `gorm:"size:128;not null"`
	ExpiresAt time.Time `gorm:"not null;index"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ResourceLock) TableName() string { return "resource_locks" }
