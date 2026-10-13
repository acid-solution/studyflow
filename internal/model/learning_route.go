package model

import "time"

type LearningRouteBinding struct {
	ID                string `gorm:"primaryKey"`
	UserID            string
	SourceRouteID     string
	GoalID            *string
	LastSourceVersion uint64
	PlanRefs          string `gorm:"type:json"`
}

type LearningRouteImport struct {
	ID             string `gorm:"primaryKey"`
	UserID         string
	IdempotencyKey string
	SourceRouteID  string
	SourceVersion  uint64
	RequestDigest  string
	Receipt        string `gorm:"type:json"`
	CreatedAt      time.Time
}
