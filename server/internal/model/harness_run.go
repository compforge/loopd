package model

import "time"

type HarnessRun struct {
	EffectKey       string    `gorm:"size:255;not null"`
	ID              string    `gorm:"primaryKey;size:36"`
	ConversationID  string    `gorm:"size:36;not null;index"`
	MessageID       string    `gorm:"size:36;not null;uniqueIndex"`
	Request         []byte    `gorm:"type:json;not null"`
	RequestHash     string    `gorm:"size:64;not null"`
	SubmissionKey   string    `gorm:"size:64;not null;uniqueIndex"`
	Target          string    `gorm:"size:128;not null"`
	Phase           string    `gorm:"size:24;not null;index:idx_harness_pending"`
	ExecutionRef    string    `gorm:"type:text"`
	ReplayHash      string    `gorm:"size:64"`
	Checkpoint      uint64    `gorm:"not null;default:0"`
	Error           string    `gorm:"type:text"`
	DeadlineAt      time.Time `gorm:"not null;index"`
	NextAttemptAt   time.Time `gorm:"not null;index:idx_harness_pending"`
	CancelRequested bool      `gorm:"not null;default:false"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (HarnessRun) TableName() string { return "harness_runs" }
