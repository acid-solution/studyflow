package studyflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"studyflow/internal/model"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CreateMilestoneInput struct {
	Title    string `json:"title"`
	Outcome  string `json:"outcome"`
	Position uint   `json:"position"`
}

type UpdateMilestoneInput struct {
	Title    *string `json:"title"`
	Outcome  *string `json:"outcome"`
	Position *uint   `json:"position"`
}

type CreateTaskInput struct {
	MilestoneID     *string `json:"milestone_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	EstimateMinutes uint    `json:"estimate_minutes"`
	ScheduledDate   *string `json:"scheduled_date"`
	Position        uint    `json:"position"`
}

type UpdateTaskInput struct {
	Version            uint    `json:"version"`
	MilestoneID        *string `json:"milestone_id"`
	ClearMilestone     bool    `json:"clear_milestone"`
	Title              *string `json:"title"`
	Description        *string `json:"description"`
	EstimateMinutes    *uint   `json:"estimate_minutes"`
	ScheduledDate      *string `json:"scheduled_date"`
	ClearScheduledDate bool    `json:"clear_scheduled_date"`
	Position           *uint   `json:"position"`
}

type TaskFilter struct {
	GoalID string
	PlanID string
	Status string
	Query  string
	From   *time.Time
	To     *time.Time
}

type SessionView struct {
	ID              string     `json:"id"`
	TaskID          string     `json:"task_id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds uint64     `json:"duration_seconds"`
	Note            string     `json:"note"`
	Status          string     `json:"status"`
}

type FinishSessionInput struct {
	Note string `json:"note"`
}

// normalizeMilestoneInput trims the free-text fields and rejects an empty title.
func normalizeMilestoneInput(input CreateMilestoneInput) (CreateMilestoneInput, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Outcome = strings.TrimSpace(input.Outcome)
	if input.Title == "" || titleTooLong(input.Title, titleLimitMilestone) {
		return input, ErrValidation
	}
	return input, nil
}

// insertMilestone writes one milestone row. It performs no lookups, no position
// allocation and no structure bump, so a caller-owned transaction can use it to
// build a whole plan tree without nesting transactions.
func insertMilestone(tx *gorm.DB, userID, versionID string, input CreateMilestoneInput, position uint) (*model.Milestone, error) {
	created := model.Milestone{ID: uuid.NewString(), UserID: userID, PlanVersionID: versionID, Title: input.Title, Outcome: input.Outcome, Position: position}
	if err := tx.Create(&created).Error; err != nil {
		if isDuplicate(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &created, nil
}

func (s *Service) CreateMilestone(ctx context.Context, userID, versionID string, input CreateMilestoneInput) (*MilestoneView, error) {
	input, err := normalizeMilestoneInput(input)
	if err != nil {
		return nil, err
	}
	var created *model.Milestone
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, _, err := s.versionAndPlan(ctx, tx, userID, versionID, true)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft {
			return ErrInvalidState
		}
		position := input.Position
		if position == 0 {
			var max uint
			if err := tx.Model(&model.Milestone{}).Where("plan_version_id = ? AND user_id = ?", versionID, userID).Select("COALESCE(MAX(position), 0)").Scan(&max).Error; err != nil {
				return err
			}
			position = max + 1
		}
		milestone, err := insertMilestone(tx, userID, versionID, input, position)
		if err != nil {
			return err
		}
		created = milestone
		return bumpStructure(tx, userID, versionID)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return &MilestoneView{ID: created.ID, Title: created.Title, Outcome: created.Outcome, Position: created.Position, Tasks: []TaskView{}}, nil
}

func (s *Service) UpdateMilestone(ctx context.Context, userID, milestoneID string, input UpdateMilestoneInput) (*MilestoneView, error) {
	var item model.Milestone
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", milestoneID, userID).First(&item).Error; err != nil {
			return mapNotFound(err)
		}
		version, _, err := s.versionAndPlan(ctx, tx, userID, item.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft {
			return ErrInvalidState
		}
		changes := map[string]any{}
		if input.Title != nil {
			value := strings.TrimSpace(*input.Title)
			if value == "" {
				return ErrValidation
			}
			changes["title"] = value
		}
		if input.Outcome != nil {
			changes["outcome"] = strings.TrimSpace(*input.Outcome)
		}
		if input.Position != nil {
			if *input.Position == 0 {
				return ErrValidation
			}
			changes["position"] = *input.Position
		}
		if len(changes) > 0 {
			if err := tx.Model(&model.Milestone{}).Where("id = ? AND user_id = ?", item.ID, userID).Updates(changes).Error; err != nil {
				if isDuplicate(err) {
					return ErrConflict
				}
				return err
			}
			if err := bumpStructure(tx, userID, item.PlanVersionID); err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND user_id = ?", milestoneID, userID).First(&item).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	var tasks []model.Task
	if err := s.db.WithContext(ctx).Where("milestone_id = ? AND user_id = ?", item.ID, userID).Order("position").Find(&tasks).Error; err != nil {
		return nil, err
	}
	view := &MilestoneView{ID: item.ID, Title: item.Title, Outcome: item.Outcome, Position: item.Position}
	for _, task := range tasks {
		view.Tasks = append(view.Tasks, taskView(task, "", "", "", time.Time{}))
	}
	return view, nil
}

func (s *Service) DeleteMilestone(ctx context.Context, userID, milestoneID string) error {
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.Milestone
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", milestoneID, userID).First(&item).Error; err != nil {
			return mapNotFound(err)
		}
		version, _, err := s.versionAndPlan(ctx, tx, userID, item.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft {
			return ErrInvalidState
		}
		if err := tx.Where("milestone_id = ? AND user_id = ?", item.ID, userID).Delete(&model.Task{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ? AND user_id = ?", item.ID, userID).Delete(&model.Milestone{}).Error; err != nil {
			return err
		}
		return bumpStructure(tx, userID, item.PlanVersionID)
	}); err != nil {
		return err
	}
	s.invalidate(ctx, userID)
	return nil
}

// normalizeTaskInput trims the free-text fields, rejects an empty title and
// parses the optional scheduled date.
func normalizeTaskInput(input CreateTaskInput) (CreateTaskInput, *time.Time, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.Title == "" || titleTooLong(input.Title, titleLimitTask) {
		return input, nil, ErrValidation
	}
	date, err := parseOptionalDate(input.ScheduledDate)
	if err != nil {
		return input, nil, err
	}
	return input, date, nil
}

// validateTaskSchedule enforces the rule that a sequence plan advances lesson by
// lesson and therefore cannot carry a scheduled date.
func validateTaskSchedule(mode string, date *time.Time) error {
	if mode == model.PlanModeSequence && date != nil {
		return fmt.Errorf("%w: sequence plan cannot schedule a date", ErrValidation)
	}
	return nil
}

// insertTask writes one task row. Same contract as insertMilestone: no lookups,
// no position allocation, no structure bump. The source records whether a person
// or an agent put it there.
func insertTask(tx *gorm.DB, userID, versionID string, input CreateTaskInput, date *time.Time, position uint, source string) (*model.Task, error) {
	created := model.Task{ID: uuid.NewString(), UserID: userID, PlanVersionID: versionID, MilestoneID: input.MilestoneID, Title: input.Title, Description: input.Description, EstimateMinutes: input.EstimateMinutes, ScheduledDate: date, Position: position, Status: model.TaskStatusTodo, Version: 1, Source: source}
	if err := tx.Create(&created).Error; err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *Service) CreateTask(ctx context.Context, userID, versionID string, input CreateTaskInput) (*TaskView, error) {
	input, date, err := normalizeTaskInput(input)
	if err != nil {
		return nil, err
	}
	var created *model.Task
	var plan model.Plan
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, foundPlan, err := s.versionAndPlan(ctx, tx, userID, versionID, true)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if version.Status != model.PlanVersionDraft && version.Status != model.PlanVersionActive {
			return ErrInvalidState
		}
		if err := validateTaskSchedule(plan.Mode, date); err != nil {
			return err
		}
		if input.MilestoneID != nil {
			var count int64
			if err := tx.Model(&model.Milestone{}).Where("id = ? AND user_id = ? AND plan_version_id = ?", *input.MilestoneID, userID, versionID).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return ErrNotFound
			}
		}
		position := input.Position
		if position == 0 {
			var max uint
			query := tx.Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ?", versionID, userID)
			if input.MilestoneID == nil {
				query = query.Where("milestone_id IS NULL")
			} else {
				query = query.Where("milestone_id = ?", *input.MilestoneID)
			}
			if err := query.Select("COALESCE(MAX(position), 0)").Scan(&max).Error; err != nil {
				return err
			}
			position = max + 1
		}
		task, err := insertTask(tx, userID, versionID, input, date, position, model.SourceUser)
		if err != nil {
			return err
		}
		created = task
		return bumpStructure(tx, userID, versionID)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(*created, plan.ID, plan.Title, plan.Mode, time.Time{})
	return &view, nil
}

func (s *Service) UpdateTask(ctx context.Context, userID, taskID string, input UpdateTaskInput) (*TaskView, error) {
	if input.Version == 0 {
		return nil, ErrValidation
	}
	var updated model.Task
	var plan model.Plan
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
			return mapNotFound(err)
		}
		if task.Version != input.Version {
			return ErrVersionConflict
		}
		version, foundPlan, err := s.versionAndPlan(ctx, tx, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if version.Status != model.PlanVersionDraft && version.Status != model.PlanVersionActive {
			return ErrInvalidState
		}
		changes := map[string]any{"version": gorm.Expr("version + 1")}
		structureChanged := false
		if input.Title != nil {
			value := strings.TrimSpace(*input.Title)
			if value == "" {
				return ErrValidation
			}
			changes["title"] = value
			structureChanged = true
		}
		if input.Description != nil {
			changes["description"] = strings.TrimSpace(*input.Description)
			structureChanged = true
		}
		if input.EstimateMinutes != nil {
			changes["estimate_minutes"] = *input.EstimateMinutes
			structureChanged = true
		}
		if input.Position != nil {
			if *input.Position == 0 {
				return ErrValidation
			}
			changes["position"] = *input.Position
			structureChanged = true
		}
		if input.ClearMilestone {
			changes["milestone_id"] = nil
			structureChanged = true
		} else if input.MilestoneID != nil {
			var count int64
			if err := tx.Model(&model.Milestone{}).Where("id = ? AND user_id = ? AND plan_version_id = ?", *input.MilestoneID, userID, task.PlanVersionID).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return ErrNotFound
			}
			changes["milestone_id"] = *input.MilestoneID
			structureChanged = true
		}
		oldDate := task.ScheduledDate
		dateChanged := false
		if input.ClearScheduledDate {
			changes["scheduled_date"] = nil
			dateChanged = task.ScheduledDate != nil
		} else if input.ScheduledDate != nil {
			date, err := parseDate(*input.ScheduledDate)
			if err != nil {
				return err
			}
			if plan.Mode == model.PlanModeSequence {
				return fmt.Errorf("%w: sequence plan cannot schedule a date", ErrValidation)
			}
			changes["scheduled_date"] = date
			dateChanged = task.ScheduledDate == nil || !sameDate(*task.ScheduledDate, *date)
		}
		result := tx.Model(&model.Task{}).Where("id = ? AND user_id = ? AND version = ?", taskID, userID, input.Version).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVersionConflict
		}
		if dateChanged {
			var newDate *time.Time
			if input.ScheduledDate != nil {
				newDate, _ = parseDate(*input.ScheduledDate)
			}
			event := model.TaskScheduleEvent{ID: uuid.NewString(), UserID: userID, TaskID: taskID, OldScheduledDate: oldDate, NewScheduledDate: newDate, CreatedAt: s.now().UTC()}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			structureChanged = true
		}
		if structureChanged {
			if err := bumpStructure(tx, userID, task.PlanVersionID); err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND user_id = ?", taskID, userID).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(updated, plan.ID, plan.Title, plan.Mode, time.Time{})
	return &view, nil
}

func (s *Service) DeleteTask(ctx context.Context, userID, taskID string) error {
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
			return mapNotFound(err)
		}
		version, _, err := s.versionAndPlan(ctx, tx, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft {
			return ErrInvalidState
		}
		if err := tx.Where("id = ? AND user_id = ?", task.ID, userID).Delete(&model.Task{}).Error; err != nil {
			return err
		}
		return bumpStructure(tx, userID, task.PlanVersionID)
	}); err != nil {
		return err
	}
	s.invalidate(ctx, userID)
	return nil
}

func (s *Service) SetTaskStatus(ctx context.Context, userID, taskID, action string) (*TaskView, error) {
	var updated model.Task
	var plan model.Plan
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
			return mapNotFound(err)
		}
		version, foundPlan, err := s.versionAndPlan(ctx, tx, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if version.Status != model.PlanVersionActive {
			return ErrInvalidState
		}
		now := s.now().UTC()
		switch action {
		case "complete":
			if task.Status == model.TaskStatusDone {
				updated = task
				return nil
			}
			if task.Status == model.TaskStatusCanceled {
				return ErrInvalidState
			}
			if err := finishRunningForTask(tx, userID, taskID, now); err != nil {
				return err
			}
			if err := tx.Model(&model.Task{}).Where("id = ? AND user_id = ?", task.ID, userID).Updates(map[string]any{"status": model.TaskStatusDone, "completed_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		case "reopen":
			if task.Status != model.TaskStatusDone {
				return ErrInvalidState
			}
			if err := tx.Model(&model.Task{}).Where("id = ? AND user_id = ?", task.ID, userID).Updates(map[string]any{"status": model.TaskStatusTodo, "completed_at": nil, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		case "cancel":
			if task.Status == model.TaskStatusCanceled {
				updated = task
				return nil
			}
			var running int64
			if err := tx.Model(&model.StudySession{}).Where("task_id = ? AND user_id = ? AND status = ?", taskID, userID, model.SessionStatusRunning).Count(&running).Error; err != nil {
				return err
			}
			if running > 0 {
				return ErrConflict
			}
			if err := tx.Model(&model.Task{}).Where("id = ? AND user_id = ?", task.ID, userID).Updates(map[string]any{"status": model.TaskStatusCanceled, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		default:
			return ErrValidation
		}
		return tx.Where("id = ? AND user_id = ?", taskID, userID).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(updated, plan.ID, plan.Title, plan.Mode, time.Time{})
	return &view, nil
}

func (s *Service) StartSession(ctx context.Context, userID, taskID string) (*SessionView, error) {
	var session model.StudySession
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
			return mapNotFound(err)
		}
		version, plan, err := s.versionAndPlan(ctx, tx, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionActive || plan.ActiveVersionID == nil || *plan.ActiveVersionID != version.ID {
			return ErrInvalidState
		}
		if task.Status == model.TaskStatusDone || task.Status == model.TaskStatusCanceled {
			return ErrInvalidState
		}
		slot := uint8(1)
		now := s.now().UTC()
		session = model.StudySession{ID: uuid.NewString(), UserID: userID, TaskID: taskID, StartedAt: now, Status: model.SessionStatusRunning, RunningSlot: &slot, Note: ""}
		if err := tx.Create(&session).Error; err != nil {
			if isDuplicate(err) {
				return ErrConflict
			}
			return err
		}
		if task.Status == model.TaskStatusTodo {
			if err := tx.Model(&model.Task{}).Where("id = ? AND user_id = ?", task.ID, userID).Updates(map[string]any{"status": model.TaskStatusInProgress, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := sessionView(session)
	return &view, nil
}

func (s *Service) ListTaskSessions(ctx context.Context, userID, taskID string) ([]SessionView, error) {
	var taskCount int64
	if err := s.db.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND user_id = ?", taskID, userID).Count(&taskCount).Error; err != nil {
		return nil, err
	}
	if taskCount != 1 {
		return nil, ErrNotFound
	}
	var sessions []model.StudySession
	if err := s.db.WithContext(ctx).Where("task_id = ? AND user_id = ?", taskID, userID).Order("started_at DESC, id DESC").Find(&sessions).Error; err != nil {
		return nil, err
	}
	result := make([]SessionView, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, sessionView(session))
	}
	return result, nil
}

func (s *Service) FinishSession(ctx context.Context, userID, sessionID string, input FinishSessionInput) (*SessionView, error) {
	return s.endSession(ctx, userID, sessionID, model.SessionStatusFinished, strings.TrimSpace(input.Note))
}

func (s *Service) DiscardSession(ctx context.Context, userID, sessionID string) (*SessionView, error) {
	return s.endSession(ctx, userID, sessionID, model.SessionStatusDiscarded, "")
}

func (s *Service) endSession(ctx context.Context, userID, sessionID, status, note string) (*SessionView, error) {
	var session model.StudySession
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
			return mapNotFound(err)
		}
		if session.Status != model.SessionStatusRunning {
			return ErrInvalidState
		}
		now := s.now().UTC()
		duration := uint64(0)
		if status == model.SessionStatusFinished && now.After(session.StartedAt) {
			duration = uint64(now.Sub(session.StartedAt).Seconds())
		}
		if err := tx.Model(&model.StudySession{}).Where("id = ? AND user_id = ?", session.ID, userID).Updates(map[string]any{"status": status, "ended_at": now, "duration_seconds": duration, "note": note, "running_slot": nil}).Error; err != nil {
			return err
		}
		if status == model.SessionStatusDiscarded {
			var finished int64
			if err := tx.Model(&model.StudySession{}).Where("task_id = ? AND user_id = ? AND status = ?", session.TaskID, userID, model.SessionStatusFinished).Count(&finished).Error; err != nil {
				return err
			}
			if finished == 0 {
				if err := tx.Model(&model.Task{}).Where("id = ? AND user_id = ? AND status = ?", session.TaskID, userID, model.TaskStatusInProgress).Updates(map[string]any{"status": model.TaskStatusTodo, "version": gorm.Expr("version + 1")}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := sessionView(session)
	return &view, nil
}

// ListTasks returns the caller's tasks, reading the unfiltered list through the
// cache.
//
// Only the unfiltered case is cached: it is what the dashboard and the task page
// both ask for, and it is the expensive one. A filtered query is answered
// directly, which keeps the cache key from having to encode the filter and keeps
// filtered reads from filling the cache with near-duplicates.
//
// The key carries the caller's local date because is_overdue is computed from it:
// without the date an entry written before midnight would keep serving yesterday's
// overdue flags until it expired.
func (s *Service) ListTasks(ctx context.Context, userID string, filter TaskFilter) ([]TaskView, error) {
	pref, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(pref.Timezone)
	if err != nil {
		location = time.UTC
	}
	today := dateOnly(s.now().In(location))
	generation, known := s.cache.Generation(ctx, userID)
	cacheable := filter == TaskFilter{} && known
	key := taskListKey(userID, today.Format("2006-01-02"), generation)
	if cacheable {
		var cached []TaskView
		if s.cache.Read(ctx, key, &cached) {
			return cached, nil
		}
	}
	items, err := s.loadTasks(ctx, userID, filter, today)
	if err != nil {
		return nil, err
	}
	if cacheable {
		s.cache.Write(ctx, key, items)
	}
	return items, nil
}

func (s *Service) loadTasks(ctx context.Context, userID string, filter TaskFilter, today time.Time) ([]TaskView, error) {
	type row struct {
		model.Task
		PlanID    string
		PlanTitle string
		PlanMode  string
	}
	query := s.db.WithContext(ctx).Table("tasks t").Select("t.*, p.id AS plan_id, p.title AS plan_title, p.mode AS plan_mode").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id").Where("t.user_id = ?", userID)
	if filter.GoalID != "" {
		query = query.Where("p.goal_id = ?", filter.GoalID)
	}
	if filter.PlanID != "" {
		query = query.Where("p.id = ?", filter.PlanID)
	}
	if filter.Status != "" {
		query = query.Where("t.status = ?", filter.Status)
	}
	if filter.Query != "" {
		query = query.Where("t.title LIKE ?", "%"+strings.TrimSpace(filter.Query)+"%")
	}
	if filter.From != nil {
		query = query.Where("t.scheduled_date >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("t.scheduled_date <= ?", *filter.To)
	}
	var rows []row
	if err := query.Order("t.scheduled_date IS NULL, t.scheduled_date, p.title, t.position, t.id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]TaskView, 0, len(rows))
	for _, value := range rows {
		result = append(result, taskView(value.Task, value.PlanID, value.PlanTitle, value.PlanMode, today))
	}
	return result, nil
}

type Dashboard struct {
	Date           string              `json:"date"`
	Overdue        []TaskView          `json:"overdue"`
	Today          []TaskView          `json:"today"`
	SequencePlans  []SequenceNextView  `json:"sequence_plans"`
	Future         []TaskView          `json:"future"`
	RunningSession *RunningSessionView `json:"running_session"`
}

type SequenceNextView struct {
	PlanID     string    `json:"plan_id"`
	PlanTitle  string    `json:"plan_title"`
	DoneTasks  int       `json:"done_tasks"`
	TotalTasks int       `json:"total_tasks"`
	NextTask   *TaskView `json:"next_task"`
}

type RunningSessionView struct {
	Session SessionView `json:"session"`
	Task    TaskView    `json:"task"`
}

func (s *Service) TodayDashboard(ctx context.Context, userID string) (*Dashboard, error) {
	pref, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(pref.Timezone)
	if err != nil {
		return nil, err
	}
	today := dateOnly(s.now().In(location))
	futureEnd := today.AddDate(0, 0, 30)
	tasks, err := s.ListTasks(ctx, userID, TaskFilter{})
	if err != nil {
		return nil, err
	}
	dashboard := &Dashboard{Date: today.Format("2006-01-02"), Overdue: []TaskView{}, Today: []TaskView{}, SequencePlans: []SequenceNextView{}, Future: []TaskView{}}
	sequenceMap := map[string]*SequenceNextView{}
	for _, task := range tasks {
		if task.PlanMode == model.PlanModeSequence {
			entry := sequenceMap[task.PlanID]
			if entry == nil {
				entry = &SequenceNextView{PlanID: task.PlanID, PlanTitle: task.PlanTitle}
				sequenceMap[task.PlanID] = entry
			}
			if task.Status != model.TaskStatusCanceled {
				entry.TotalTasks++
			}
			if task.Status == model.TaskStatusDone {
				entry.DoneTasks++
			}
			if entry.NextTask == nil && task.Status != model.TaskStatusDone && task.Status != model.TaskStatusCanceled {
				copy := task
				entry.NextTask = &copy
			}
			continue
		}
		if task.ScheduledDate == nil || task.Status == model.TaskStatusCanceled {
			continue
		}
		date, _ := time.ParseInLocation("2006-01-02", *task.ScheduledDate, location)
		if date.Before(today) && task.Status != model.TaskStatusDone {
			dashboard.Overdue = append(dashboard.Overdue, task)
		}
		if sameDate(date, today) && (pref.ShowCompletedToday || task.Status != model.TaskStatusDone) {
			dashboard.Today = append(dashboard.Today, task)
		}
		if date.After(today) && !date.After(futureEnd) && task.Status != model.TaskStatusDone {
			dashboard.Future = append(dashboard.Future, task)
		}
	}
	keys := make([]string, 0, len(sequenceMap))
	for key := range sequenceMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		dashboard.SequencePlans = append(dashboard.SequencePlans, *sequenceMap[key])
	}
	sortTaskViews(dashboard.Overdue)
	sortTaskViews(dashboard.Today)
	sortTaskViews(dashboard.Future)
	type sessionRow struct {
		model.StudySession
		TaskTitle     string
		PlanID        string
		PlanTitle     string
		PlanMode      string
		PlanVersionID string
		TaskVersion   uint
		TaskStatus    string
	}
	var running sessionRow
	if err := s.db.WithContext(ctx).Table("study_sessions s").Select("s.*, t.title AS task_title, t.plan_version_id, t.version AS task_version, t.status AS task_status, p.id AS plan_id, p.title AS plan_title, p.mode AS plan_mode").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("s.user_id = ? AND s.status = ?", userID, model.SessionStatusRunning).Take(&running).Error; err == nil {
		task := TaskView{ID: running.TaskID, PlanID: running.PlanID, PlanTitle: running.PlanTitle, PlanMode: running.PlanMode, PlanVersionID: running.PlanVersionID, Title: running.TaskTitle, Status: running.TaskStatus, Version: running.TaskVersion}
		dashboard.RunningSession = &RunningSessionView{Session: sessionView(running.StudySession), Task: task}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return dashboard, nil
}

func (s *Service) versionAndPlan(ctx context.Context, db *gorm.DB, userID, versionID string, lock bool) (*model.PlanVersion, *model.Plan, error) {
	query := db.WithContext(ctx)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var version model.PlanVersion
	if err := query.Where("id = ? AND user_id = ?", versionID, userID).First(&version).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	var plan model.Plan
	if err := db.WithContext(ctx).Where("id = ? AND user_id = ?", version.PlanID, userID).First(&plan).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	return &version, &plan, nil
}

func bumpStructure(tx *gorm.DB, userID, versionID string) error {
	result := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", versionID, userID).Update("structure_revision", gorm.Expr("structure_revision + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func finishRunningForTask(tx *gorm.DB, userID, taskID string, now time.Time) error {
	var session model.StudySession
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id = ? AND user_id = ? AND status = ?", taskID, userID, model.SessionStatusRunning).First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	duration := uint64(0)
	if now.After(session.StartedAt) {
		duration = uint64(now.Sub(session.StartedAt).Seconds())
	}
	return tx.Model(&model.StudySession{}).Where("id = ? AND user_id = ?", session.ID, userID).Updates(map[string]any{"status": model.SessionStatusFinished, "ended_at": now, "duration_seconds": duration, "running_slot": nil}).Error
}

func sessionView(session model.StudySession) SessionView {
	return SessionView{ID: session.ID, TaskID: session.TaskID, StartedAt: session.StartedAt, EndedAt: session.EndedAt, DurationSeconds: session.DurationSeconds, Note: session.Note, Status: session.Status}
}
func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}
func sameDate(left, right time.Time) bool {
	return left.Year() == right.Year() && left.YearDay() == right.YearDay()
}
