package studyflow

import (
	"context"
	"errors"
	"strings"
	"time"

	"studyflow/internal/model"
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
	value, err := s.repo.Preference(ctx, userID)
	if err == nil {
		return preferenceView(*value), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	now := s.now().UTC()
	created := model.UserPreference{UserID: userID, Timezone: "Asia/Shanghai", WeekStart: "monday", DefaultPlanMode: model.PlanModeCalendar, ShowCompletedToday: true, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreatePreferenceIfMissing(ctx, &created); err != nil {
		return nil, err
	}
	value, err = s.repo.Preference(ctx, userID)
	if err != nil {
		return nil, err
	}
	return preferenceView(*value), nil
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
	if err := s.repo.UpdatePreference(ctx, userID, changes); err != nil {
		return nil, err
	}
	// Timezone and week start decide how the cached task list marks overdue items
	// and how the cached review buckets days, so this write has to invalidate too.
	s.invalidate(ctx, userID)
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

	// Keyed by the resolved week rather than the request, so "this week" stops
	// being served from the cache the moment the week rolls over, even if nothing
	// was written in between.
	generation, known := s.cache.Generation(ctx, userID)
	key := weeklyReviewKey(userID, startLocal.Format("2006-01-02"), generation)
	if known {
		var cached WeeklyReview
		if s.cache.Read(ctx, key, &cached) {
			return &cached, nil
		}
	}
	review, err := s.loadWeeklyReview(ctx, userID, location, startLocal)
	if err != nil {
		return nil, err
	}
	if known {
		s.cache.Write(ctx, key, review)
	}
	return review, nil
}

// loadWeeklyReview runs the week's aggregates straight from MySQL.
func (s *Service) loadWeeklyReview(ctx context.Context, userID string, location *time.Location, startLocal time.Time) (*WeeklyReview, error) {
	endLocal := startLocal.AddDate(0, 0, 7)
	startUTC, endUTC := startLocal.UTC(), endLocal.UTC()
	review := &WeeklyReview{WeekStart: startLocal.Format("2006-01-02"), WeekEnd: endLocal.AddDate(0, 0, -1).Format("2006-01-02"), Daily: make([]DailyInvestment, 7), Plans: []PlanReview{}}
	for index := range review.Daily {
		review.Daily[index].Date = startLocal.AddDate(0, 0, index).Format("2006-01-02")
	}

	activePlans, err := s.repo.ActivePlans(ctx, userID)
	if err != nil {
		return nil, err
	}
	planIndex := map[string]int{}
	for _, plan := range activePlans {
		version, err := s.repo.PlanVersion(ctx, userID, *plan.ActiveVersionID, false)
		if err != nil {
			return nil, err
		}
		planIndex[plan.ID] = len(review.Plans)
		review.Plans = append(review.Plans, PlanReview{PlanID: plan.ID, PlanTitle: plan.Title, PlannedMinutes: version.WeeklyCapacityMinutes})
		review.PlannedMinutes += version.WeeklyCapacityMinutes
	}

	sessions, err := s.repo.ReviewSessions(ctx, userID, startUTC, endUTC)
	if err != nil {
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

	completed, err := s.repo.CompletedTasks(ctx, userID, startUTC, endUTC)
	if err != nil {
		return nil, err
	}
	review.CompletedTasks = len(completed)
	for _, item := range completed {
		if index, ok := planIndex[item.PlanID]; ok {
			review.Plans[index].CompletedTasks++
		}
	}

	scheduled, err := s.repo.ScheduledTasks(ctx, userID, startLocal, endLocal)
	if err != nil {
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
	overdue, err := s.repo.CurrentOverdueCount(ctx, userID, today)
	if err != nil {
		return nil, err
	}
	review.CurrentOverdue = int(overdue)

	reschedules, err := s.repo.Reschedules(ctx, userID, startUTC, endUTC)
	if err != nil {
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
