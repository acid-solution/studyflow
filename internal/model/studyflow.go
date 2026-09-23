package model

import "time"

const (
	GoalStatusActive    = "active"
	GoalStatusAchieved  = "achieved"
	GoalStatusAbandoned = "abandoned"

	PlanModeCalendar = "calendar"
	PlanModeSequence = "sequence"

	PlanVersionDraft      = "draft"
	PlanVersionActive     = "active"
	PlanVersionSuperseded = "superseded"
	PlanVersionCanceled   = "canceled"

	TaskStatusTodo       = "todo"
	TaskStatusInProgress = "in_progress"
	TaskStatusDone       = "done"
	TaskStatusCanceled   = "canceled"

	SessionStatusRunning   = "running"
	SessionStatusFinished  = "finished"
	SessionStatusDiscarded = "discarded"
)

type Goal struct {
	ID              string     `gorm:"type:char(36);primaryKey"`
	UserID          string     `gorm:"type:char(36);not null;index"`
	ParentGoalID    *string    `gorm:"type:char(36);index"`
	Title           string     `gorm:"type:varchar(160);not null"`
	Description     string     `gorm:"type:text;not null"`
	SuccessCriteria string     `gorm:"type:text;not null"`
	TargetDate      *time.Time `gorm:"type:date"`
	Status          string     `gorm:"type:varchar(16);not null"`
	Version         uint       `gorm:"not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Plan struct {
	ID              string  `gorm:"type:char(36);primaryKey"`
	UserID          string  `gorm:"type:char(36);not null;index"`
	GoalID          string  `gorm:"type:char(36);not null;index"`
	ActiveVersionID *string `gorm:"type:char(36)"`
	Title           string  `gorm:"type:varchar(160);not null"`
	Description     string  `gorm:"type:text;not null"`
	Mode            string  `gorm:"type:varchar(16);not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PlanVersion struct {
	ID                    string     `gorm:"type:char(36);primaryKey"`
	UserID                string     `gorm:"type:char(36);not null;index"`
	PlanID                string     `gorm:"type:char(36);not null;index"`
	VersionNo             uint       `gorm:"not null"`
	Status                string     `gorm:"type:varchar(16);not null"`
	WeeklyCapacityMinutes uint       `gorm:"not null"`
	StartDate             *time.Time `gorm:"type:date"`
	EndDate               *time.Time `gorm:"type:date"`
	BaseVersionID         *string    `gorm:"type:char(36)"`
	BaseStructureRevision uint       `gorm:"not null"`
	StructureRevision     uint       `gorm:"not null"`
	ActivatedAt           *time.Time
	SupersededAt          *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type Milestone struct {
	ID            string `gorm:"type:char(36);primaryKey"`
	UserID        string `gorm:"type:char(36);not null;index"`
	PlanVersionID string `gorm:"type:char(36);not null;index"`
	Title         string `gorm:"type:varchar(160);not null"`
	Outcome       string `gorm:"type:text;not null"`
	Position      uint   `gorm:"not null"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Task struct {
	ID               string     `gorm:"type:char(36);primaryKey"`
	UserID           string     `gorm:"type:char(36);not null;index"`
	PlanVersionID    string     `gorm:"type:char(36);not null;index"`
	MilestoneID      *string    `gorm:"type:char(36);index"`
	CopiedFromTaskID *string    `gorm:"type:char(36)"`
	Title            string     `gorm:"type:varchar(255);not null"`
	Description      string     `gorm:"type:text;not null"`
	EstimateMinutes  uint       `gorm:"not null"`
	ScheduledDate    *time.Time `gorm:"type:date;index"`
	Position         uint       `gorm:"not null"`
	Status           string     `gorm:"type:varchar(16);not null;index"`
	Version          uint       `gorm:"not null"`
	CompletedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type StudySession struct {
	ID              string    `gorm:"type:char(36);primaryKey"`
	UserID          string    `gorm:"type:char(36);not null;index"`
	TaskID          string    `gorm:"type:char(36);not null;index"`
	StartedAt       time.Time `gorm:"not null"`
	EndedAt         *time.Time
	DurationSeconds uint64 `gorm:"not null"`
	Note            string `gorm:"type:text;not null"`
	Status          string `gorm:"type:varchar(16);not null"`
	RunningSlot     *uint8
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type TaskScheduleEvent struct {
	ID               string     `gorm:"type:char(36);primaryKey"`
	UserID           string     `gorm:"type:char(36);not null;index"`
	TaskID           string     `gorm:"type:char(36);not null;index"`
	OldScheduledDate *time.Time `gorm:"type:date"`
	NewScheduledDate *time.Time `gorm:"type:date"`
	CreatedAt        time.Time  `gorm:"not null"`
}

type UserPreference struct {
	UserID             string `gorm:"type:char(36);primaryKey"`
	Timezone           string `gorm:"type:varchar(64);not null"`
	WeekStart          string `gorm:"type:varchar(16);not null"`
	DefaultPlanMode    string `gorm:"type:varchar(16);not null"`
	ShowCompletedToday bool   `gorm:"not null"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
