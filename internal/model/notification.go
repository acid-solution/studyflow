package model

import "time"

type NotificationStream struct {
	UserID        string `gorm:"primaryKey;type:char(36)"`
	LastSequence  uint64
	PrunedThrough uint64
}

type NotificationOutbox struct {
	UserID     string `gorm:"primaryKey;type:char(36)"`
	Sequence   uint64 `gorm:"primaryKey"`
	Kind       string
	Resources  string `gorm:"type:json"`
	Topics     string `gorm:"type:json"`
	OccurredAt time.Time
}

func (NotificationOutbox) TableName() string { return "notification_outbox" }
