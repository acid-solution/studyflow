package studyflow

import (
	"context"
	"errors"
	"strings"
	"time"

	"studyflow/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PreferenceView struct {
	Timezone           string `json:"timezone"`
	WeekStart          string `json:"week_start"`
	DefaultPlanMode    string `json:"default_plan_mode"`
	ShowCompletedToday bool   `json:"show_completed_today"`
}

type UpdatePreferenceInput struct {
	Timezone           *string `json:"timezone"`
	WeekStart          *string `json:"week_start"`
	DefaultPlanMode    *string `json:"default_plan_mode"`
	ShowCompletedToday *bool   `json:"show_completed_today"`
}

type DailyInvestment struct {
	Date            string `json:"date"`
	DurationSeconds uint64 `json:"duration_seconds"`
}

type PlanReview struct {
	PlanID                string `json:"plan_id"`
	PlanTitle             string `json:"plan_title"`
	PlannedMinutes        uint   `json:"planned_minutes"`
	ActualDurationSeconds uint64 `json:"actual_duration_seconds"`
	CompletedTasks        int    `json:"completed_tasks"`
	RescheduleCount       int    `json:"reschedule_count"`
}

type WeeklyReview struct {
	WeekStart             string            `json:"week_start"`
	WeekEnd               string            `json:"week_end"`
	CompletedTasks        int               `json:"completed_tasks"`
	PlannedMinutes        uint              `json:"planned_minutes"`
	ActualDurationSeconds uint64            `json:"actual_duration_seconds"`
	OnTimeCompleted       int               `json:"on_time_completed"`
	ScheduledTasks        int               `json:"scheduled_tasks"`
	OnTimeRate            float64           `json:"on_time_rate"`
	CurrentOverdue        int               `json:"current_overdue"`
	RescheduleCount       int               `json:"reschedule_count"`
	Daily                 []DailyInvestment `json:"daily"`
	Plans                 []PlanReview      `json:"plans"`
}

func (s *Service) GetPreferences(ctx context.Context, userID string) (*PreferenceView, error) {
	var value model.UserPreference
	result := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&value)
	if result.Error == nil {
		return preferenceView(value), nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, result.Error
	}
	now := s.now().UTC()
	value = model.UserPreference{UserID: userID, Timezone: "Asia/Shanghai", WeekStart: "monday", DefaultPlanMode: model.PlanModeCalendar, ShowCompletedToday: true, CreatedAt: now, UpdatedAt: now}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&value).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&value).Error; err != nil {
		return nil, err
	}
	return preferenceView(value), nil
}

func (s *Service) UpdatePreferences(ctx context.Context, userID string, input UpdatePreferenceInput) (*PreferenceView, error) {
	current, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	changes := map[string]any{}
	if input.Timezone != nil {
		value := strings.TrimSpace(*input.Timezone)
		if _, err := time.LoadLocation(value); err != nil {
			return nil, ErrValidation
		}
		changes["timezone"] = value
	}
	if input.WeekStart != nil {
		if *input.WeekStart != "monday" && *input.WeekStart != "sunday" {
			return nil, ErrValidation
		}
		changes["week_start"] = *input.WeekStart
	}
	if input.DefaultPlanMode != nil {
		if *input.DefaultPlanMode != model.PlanModeCalendar && *input.DefaultPlanMode != model.PlanModeSequence {
			return nil, ErrValidation
		}
		changes["default_plan_mode"] = *input.DefaultPlanMode
	}
	if input.ShowCompletedToday != nil {
		changes["show_completed_today"] = *input.ShowCompletedToday
	}
	if len(changes) == 0 {
		return current, nil
	}
	if err := s.db.WithContext(ctx).Model(&model.UserPreference{}).Where("user_id = ?", userID).Updates(changes).Error; err != nil {
		return nil, err
	}
	return s.GetPreferences(ctx, userID)
}

func (s *Service) WeeklyReview(ctx context.Context, userID, requestedStart string) (*WeeklyReview, error) {
	pref, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(pref.Timezone)
	if err != nil {
		return nil, err
	}
	var startLocal time.Time
	if strings.TrimSpace(requestedStart) != "" {
		parsed, err := time.ParseInLocation("2006-01-02", requestedStart, location)
		if err != nil {
			return nil, ErrValidation
		}
		startLocal = parsed
	} else {
		startLocal = beginningOfWeek(s.now().In(location), pref.WeekStart)
	}
	startLocal = dateOnly(startLocal)
	endLocal := startLocal.AddDate(0, 0, 7)
	startUTC, endUTC := startLocal.UTC(), endLocal.UTC()
	review := &WeeklyReview{WeekStart: startLocal.Format("2006-01-02"), WeekEnd: endLocal.AddDate(0, 0, -1).Format("2006-01-02"), Daily: make([]DailyInvestment, 7), Plans: []PlanReview{}}
	for index := range review.Daily {
		review.Daily[index].Date = startLocal.AddDate(0, 0, index).Format("2006-01-02")
	}

	var activePlans []model.Plan
	if err := s.db.WithContext(ctx).Where("user_id = ? AND active_version_id IS NOT NULL", userID).Order("created_at").Find(&activePlans).Error; err != nil {
		return nil, err
	}
	planIndex := map[string]int{}
	for _, plan := range activePlans {
		var version model.PlanVersion
		if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&version).Error; err != nil {
			return nil, err
		}
		planIndex[plan.ID] = len(review.Plans)
		review.Plans = append(review.Plans, PlanReview{PlanID: plan.ID, PlanTitle: plan.Title, PlannedMinutes: version.WeeklyCapacityMinutes})
		review.PlannedMinutes += version.WeeklyCapacityMinutes
	}

	type sessionRow struct {
		DurationSeconds uint64
		StartedAt       time.Time
		PlanID          string
	}
	var sessions []sessionRow
	if err := s.db.WithContext(ctx).Table("study_sessions s").Select("s.duration_seconds, s.started_at, p.id AS plan_id").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("s.user_id = ? AND s.status = ? AND s.started_at >= ? AND s.started_at < ?", userID, model.SessionStatusFinished, startUTC, endUTC).Scan(&sessions).Error; err != nil {
		return nil, err
	}
	for _, session := range sessions {
		review.ActualDurationSeconds += session.DurationSeconds
		local := session.StartedAt.In(location)
		day := int(dateOnly(local).Sub(startLocal).Hours() / 24)
		if day >= 0 && day < len(review.Daily) {
			review.Daily[day].DurationSeconds += session.DurationSeconds
		}
		if index, ok := planIndex[session.PlanID]; ok {
			review.Plans[index].ActualDurationSeconds += session.DurationSeconds
		}
	}

	type completedRow struct {
		CompletedAt   time.Time
		ScheduledDate *time.Time
		PlanID        string
		PlanMode      string
	}
	var completed []completedRow
	if err := s.db.WithContext(ctx).Table("tasks t").Select("t.completed_at, t.scheduled_date, p.id AS plan_id, p.mode AS plan_mode").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("t.user_id = ? AND t.status = ? AND t.completed_at >= ? AND t.completed_at < ?", userID, model.TaskStatusDone, startUTC, endUTC).Scan(&completed).Error; err != nil {
		return nil, err
	}
	review.CompletedTasks = len(completed)
	for _, item := range completed {
		if index, ok := planIndex[item.PlanID]; ok {
			review.Plans[index].CompletedTasks++
		}
	}

	type scheduledRow struct {
		ScheduledDate time.Time
		CompletedAt   *time.Time
	}
	var scheduled []scheduledRow
	if err := s.db.WithContext(ctx).Table("tasks t").Select("t.scheduled_date, t.completed_at").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id").Where("t.user_id = ? AND p.mode = ? AND t.status <> ? AND t.scheduled_date >= ? AND t.scheduled_date < ?", userID, model.PlanModeCalendar, model.TaskStatusCanceled, startLocal, endLocal).Scan(&scheduled).Error; err != nil {
		return nil, err
	}
	review.ScheduledTasks = len(scheduled)
	for _, item := range scheduled {
		if item.CompletedAt != nil {
			completedDate := dateOnly(item.CompletedAt.In(location))
			dueDate := dateOnly(item.ScheduledDate.In(location))
			if !completedDate.After(dueDate) {
				review.OnTimeCompleted++
			}
		}
	}
	if review.ScheduledTasks > 0 {
		review.OnTimeRate = float64(review.OnTimeCompleted) / float64(review.ScheduledTasks)
	}

	today := dateOnly(s.now().In(location))
	var overdue int64
	if err := s.db.WithContext(ctx).Table("tasks t").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id").Where("t.user_id = ? AND p.mode = ? AND t.status IN ? AND t.scheduled_date < ?", userID, model.PlanModeCalendar, []string{model.TaskStatusTodo, model.TaskStatusInProgress}, today).Count(&overdue).Error; err != nil {
		return nil, err
	}
	review.CurrentOverdue = int(overdue)

	type rescheduleRow struct {
		PlanID string
		Count  int
	}
	var reschedules []rescheduleRow
	if err := s.db.WithContext(ctx).Table("task_schedule_events e").Select("p.id AS plan_id, COUNT(*) AS count").Joins("JOIN tasks t ON t.id = e.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("e.user_id = ? AND e.created_at >= ? AND e.created_at < ?", userID, startUTC, endUTC).Group("p.id").Scan(&reschedules).Error; err != nil {
		return nil, err
	}
	for _, item := range reschedules {
		review.RescheduleCount += item.Count
		if index, ok := planIndex[item.PlanID]; ok {
			review.Plans[index].RescheduleCount += item.Count
		}
	}
	return review, nil
}

func preferenceView(value model.UserPreference) *PreferenceView {
	return &PreferenceView{Timezone: value.Timezone, WeekStart: value.WeekStart, DefaultPlanMode: value.DefaultPlanMode, ShowCompletedToday: value.ShowCompletedToday}
}
func beginningOfWeek(value time.Time, weekStart string) time.Time {
	start := time.Monday
	if weekStart == "sunday" {
		start = time.Sunday
	}
	delta := (int(value.Weekday()) - int(start) + 7) % 7
	return dateOnly(value).AddDate(0, 0, -delta)
}
