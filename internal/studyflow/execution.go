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
	RequireCurrentPlan bool    `json:"-"`
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
func insertMilestone(ctx context.Context, repo Repository, userID, versionID string, input CreateMilestoneInput, position uint) (*model.Milestone, error) {
	created := model.Milestone{ID: uuid.NewString(), UserID: userID, PlanVersionID: versionID, Title: input.Title, Outcome: input.Outcome, Position: position}
	if err := repo.CreateMilestone(ctx, &created); err != nil {
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
	err = s.repo.WithinTransaction(ctx, func(repo Repository) error {
		version, plan, err := s.versionAndPlan(ctx, repo, userID, versionID, true)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		position := input.Position
		if position == 0 {
			max, err := repo.MaxMilestonePosition(ctx, userID, versionID)
			if err != nil {
				return err
			}
			position = max + 1
		}
		milestone, err := insertMilestone(ctx, repo, userID, versionID, input, position)
		if err != nil {
			return err
		}
		created = milestone
		return repo.BumpStructure(ctx, userID, versionID)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return &MilestoneView{ID: created.ID, Title: created.Title, Outcome: created.Outcome, Position: created.Position, Tasks: []TaskView{}}, nil
}

func (s *Service) UpdateMilestone(ctx context.Context, userID, milestoneID string, input UpdateMilestoneInput) (*MilestoneView, error) {
	var item model.Milestone
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		found, err := repo.Milestone(ctx, userID, milestoneID, true)
		if err != nil {
			return err
		}
		item = *found
		version, plan, err := s.versionAndPlan(ctx, repo, userID, item.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
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
			if err := repo.UpdateMilestone(ctx, userID, item.ID, changes); err != nil {
				return err
			}
			if err := repo.BumpStructure(ctx, userID, item.PlanVersionID); err != nil {
				return err
			}
		}
		found, err = repo.Milestone(ctx, userID, milestoneID, false)
		if err == nil {
			item = *found
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	tasks, err := s.repo.TasksByMilestone(ctx, userID, item.ID)
	if err != nil {
		return nil, err
	}
	view := &MilestoneView{ID: item.ID, Title: item.Title, Outcome: item.Outcome, Position: item.Position}
	for _, task := range tasks {
		view.Tasks = append(view.Tasks, taskView(task, "", "", "", time.Time{}))
	}
	return view, nil
}

func (s *Service) DeleteMilestone(ctx context.Context, userID, milestoneID string) error {
	if err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		item, err := repo.Milestone(ctx, userID, milestoneID, true)
		if err != nil {
			return err
		}
		version, plan, err := s.versionAndPlan(ctx, repo, userID, item.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		if err := repo.DeleteMilestoneWithTasks(ctx, userID, item.ID); err != nil {
			return err
		}
		return repo.BumpStructure(ctx, userID, item.PlanVersionID)
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
func insertTask(ctx context.Context, repo Repository, userID, versionID string, input CreateTaskInput, date *time.Time, position uint, source string) (*model.Task, error) {
	created := model.Task{ID: uuid.NewString(), UserID: userID, PlanVersionID: versionID, MilestoneID: input.MilestoneID, Title: input.Title, Description: input.Description, EstimateMinutes: input.EstimateMinutes, ScheduledDate: date, Position: position, Status: model.TaskStatusTodo, Version: 1, Source: source}
	if err := repo.CreateTask(ctx, &created); err != nil {
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
	err = s.repo.WithinTransaction(ctx, func(repo Repository) error {
		version, foundPlan, err := s.versionAndPlan(ctx, repo, userID, versionID, true)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if plan.Status != model.GoalStatusActive || (version.Status != model.PlanVersionDraft && version.Status != model.PlanVersionActive) {
			return ErrInvalidState
		}
		if err := validateTaskSchedule(version.Mode, date); err != nil {
			return err
		}
		if input.MilestoneID != nil {
			exists, err := repo.MilestoneExists(ctx, userID, versionID, *input.MilestoneID)
			if err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		position := input.Position
		if position == 0 {
			max, err := repo.MaxTaskPosition(ctx, userID, versionID, input.MilestoneID)
			if err != nil {
				return err
			}
			position = max + 1
		}
		task, err := insertTask(ctx, repo, userID, versionID, input, date, position, model.SourceUser)
		if err != nil {
			return err
		}
		created = task
		return repo.BumpStructure(ctx, userID, versionID)
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(*created, plan.ID, plan.Title, versionMode(ctx, s.repo, userID, created.PlanVersionID), time.Time{})
	return &view, nil
}

func (s *Service) UpdateTask(ctx context.Context, userID, taskID string, input UpdateTaskInput) (*TaskView, error) {
	if input.Version == 0 {
		return nil, ErrValidation
	}
	var updated model.Task
	var plan model.Plan
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		task, err := repo.Task(ctx, userID, taskID, true)
		if err != nil {
			return err
		}
		if task.Version != input.Version {
			return ErrVersionConflict
		}
		version, foundPlan, err := s.versionAndPlan(ctx, repo, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if plan.Status != model.GoalStatusActive || (version.Status != model.PlanVersionDraft && version.Status != model.PlanVersionActive) {
			return ErrInvalidState
		}
		if input.RequireCurrentPlan && (version.Status != model.PlanVersionActive || plan.ActiveVersionID == nil || *plan.ActiveVersionID != version.ID) {
			return ErrInvalidState
		}
		changes := map[string]any{}
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
			exists, err := repo.MilestoneExists(ctx, userID, task.PlanVersionID, *input.MilestoneID)
			if err != nil {
				return err
			}
			if !exists {
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
			if version.Mode == model.PlanModeSequence {
				return fmt.Errorf("%w: sequence plan cannot schedule a date", ErrValidation)
			}
			changes["scheduled_date"] = date
			dateChanged = task.ScheduledDate == nil || !sameDate(*task.ScheduledDate, *date)
		}
		ok, err := repo.UpdateTask(ctx, userID, taskID, &input.Version, changes, true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrVersionConflict
		}
		if dateChanged {
			var newDate *time.Time
			if input.ScheduledDate != nil {
				newDate, _ = parseDate(*input.ScheduledDate)
			}
			event := model.TaskScheduleEvent{ID: uuid.NewString(), UserID: userID, TaskID: taskID, OldScheduledDate: oldDate, NewScheduledDate: newDate, CreatedAt: s.now().UTC()}
			if err := repo.CreateScheduleEvent(ctx, &event); err != nil {
				return err
			}
			structureChanged = true
		}
		if structureChanged {
			if err := repo.BumpStructure(ctx, userID, task.PlanVersionID); err != nil {
				return err
			}
		}
		value, err := repo.Task(ctx, userID, taskID, false)
		if err == nil {
			updated = *value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(updated, plan.ID, plan.Title, versionMode(ctx, s.repo, userID, updated.PlanVersionID), time.Time{})
	return &view, nil
}

func (s *Service) DeleteTask(ctx context.Context, userID, taskID string) error {
	if err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		task, err := repo.Task(ctx, userID, taskID, true)
		if err != nil {
			return err
		}
		version, plan, err := s.versionAndPlan(ctx, repo, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		if err := repo.DeleteTask(ctx, userID, task.ID); err != nil {
			return err
		}
		return repo.BumpStructure(ctx, userID, task.PlanVersionID)
	}); err != nil {
		return err
	}
	s.invalidate(ctx, userID)
	return nil
}

func (s *Service) SetTaskStatus(ctx context.Context, userID, taskID, action string) (*TaskView, error) {
	var updated model.Task
	var plan model.Plan
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		task, err := repo.Task(ctx, userID, taskID, true)
		if err != nil {
			return err
		}
		version, foundPlan, err := s.versionAndPlan(ctx, repo, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		plan = *foundPlan
		if version.Status != model.PlanVersionActive || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		now := s.now().UTC()
		switch action {
		case "complete":
			if task.Status == model.TaskStatusDone {
				updated = *task
				return nil
			}
			if task.Status == model.TaskStatusCanceled {
				return ErrInvalidState
			}
			if err := finishRunningForTask(ctx, repo, userID, taskID, now); err != nil {
				return err
			}
			if _, err := repo.UpdateTask(ctx, userID, task.ID, nil, map[string]any{"status": model.TaskStatusDone, "completed_at": now}, true); err != nil {
				return err
			}
		case "reopen":
			if task.Status != model.TaskStatusDone {
				return ErrInvalidState
			}
			if _, err := repo.UpdateTask(ctx, userID, task.ID, nil, map[string]any{"status": model.TaskStatusTodo, "completed_at": nil}, true); err != nil {
				return err
			}
		case "cancel":
			if task.Status == model.TaskStatusCanceled {
				updated = *task
				return nil
			}
			running, err := repo.CountSessions(ctx, userID, taskID, model.SessionStatusRunning)
			if err != nil {
				return err
			}
			if running > 0 {
				return ErrConflict
			}
			if _, err := repo.UpdateTask(ctx, userID, task.ID, nil, map[string]any{"status": model.TaskStatusCanceled}, true); err != nil {
				return err
			}
		default:
			return ErrValidation
		}
		value, err := repo.Task(ctx, userID, taskID, false)
		if err == nil {
			updated = *value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view := taskView(updated, plan.ID, plan.Title, versionMode(ctx, s.repo, userID, updated.PlanVersionID), time.Time{})
	return &view, nil
}

func (s *Service) StartSession(ctx context.Context, userID, taskID string) (*SessionView, error) {
	var session model.StudySession
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		task, err := repo.Task(ctx, userID, taskID, true)
		if err != nil {
			return err
		}
		version, plan, err := s.versionAndPlan(ctx, repo, userID, task.PlanVersionID, false)
		if err != nil {
			return err
		}
		if plan.Status != model.GoalStatusActive || version.Status != model.PlanVersionActive || plan.ActiveVersionID == nil || *plan.ActiveVersionID != version.ID {
			return ErrInvalidState
		}
		if task.Status == model.TaskStatusDone || task.Status == model.TaskStatusCanceled {
			return ErrInvalidState
		}
		slot := uint8(1)
		now := s.now().UTC()
		session = model.StudySession{ID: uuid.NewString(), UserID: userID, TaskID: taskID, StartedAt: now, Status: model.SessionStatusRunning, RunningSlot: &slot, Note: ""}
		if err := repo.CreateSession(ctx, &session); err != nil {
			return err
		}
		if task.Status == model.TaskStatusTodo {
			if _, err := repo.UpdateTask(ctx, userID, task.ID, nil, map[string]any{"status": model.TaskStatusInProgress}, true); err != nil {
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
	exists, err := s.repo.TaskExists(ctx, userID, taskID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	sessions, err := s.repo.SessionsByTask(ctx, userID, taskID)
	if err != nil {
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
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		found, err := repo.Session(ctx, userID, sessionID, true)
		if err != nil {
			return err
		}
		session = *found
		if session.Status != model.SessionStatusRunning {
			return ErrInvalidState
		}
		now := s.now().UTC()
		duration := uint64(0)
		if status == model.SessionStatusFinished && now.After(session.StartedAt) {
			duration = uint64(now.Sub(session.StartedAt).Seconds())
		}
		if err := repo.UpdateSession(ctx, userID, session.ID, map[string]any{"status": status, "ended_at": now, "duration_seconds": duration, "note": note, "running_slot": nil}); err != nil {
			return err
		}
		if status == model.SessionStatusDiscarded {
			finished, err := repo.CountSessions(ctx, userID, session.TaskID, model.SessionStatusFinished)
			if err != nil {
				return err
			}
			if finished == 0 {
				if err := repo.ResetTaskIfInProgress(ctx, userID, session.TaskID); err != nil {
					return err
				}
			}
		}
		found, err = repo.Session(ctx, userID, sessionID, false)
		if err == nil {
			session = *found
		}
		return err
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
		s.cache.Write(ctx, userID, generation, key, items)
	}
	return items, nil
}

func (s *Service) loadTasks(ctx context.Context, userID string, filter TaskFilter, today time.Time) ([]TaskView, error) {
	rows, err := s.repo.ListTaskRecords(ctx, userID, filter)
	if err != nil {
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
	CompletedToday []TaskView          `json:"completed_today"`
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
	dashboard := &Dashboard{Date: today.Format("2006-01-02"), Overdue: []TaskView{}, Today: []TaskView{}, CompletedToday: []TaskView{}, SequencePlans: []SequenceNextView{}, Future: []TaskView{}}
	sequenceMap := map[string]*SequenceNextView{}
	for _, task := range tasks {
		// Finished today is defined by when it was finished, not by where it was
		// scheduled. A task due yesterday that was only finished today belongs here,
		// and so does a lesson from a sequence plan, which carries no date at all —
		// so this is checked before the branches that skip undated and past tasks.
		if pref.ShowCompletedToday && task.Status == model.TaskStatusDone && task.CompletedAt != nil && sameDate(task.CompletedAt.In(location), today) {
			dashboard.CompletedToday = append(dashboard.CompletedToday, task)
		}
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
		// Today holds only what is still open, which is what the summary calls it.
		// Finished tasks have their own section, so a task is never listed twice.
		if sameDate(date, today) && task.Status != model.TaskStatusDone {
			dashboard.Today = append(dashboard.Today, task)
		}
		if date.After(today) && !date.After(futureEnd) && task.Status != model.TaskStatusDone {
			dashboard.Future = append(dashboard.Future, task)
		}
	}
	// Most recently finished first.
	sort.Slice(dashboard.CompletedToday, func(i, j int) bool {
		return dashboard.CompletedToday[i].CompletedAt.After(*dashboard.CompletedToday[j].CompletedAt)
	})
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
	running, err := s.repo.RunningSessionRecord(ctx, userID)
	if err == nil {
		task := TaskView{ID: running.TaskID, PlanID: running.PlanID, PlanTitle: running.PlanTitle, PlanMode: running.PlanMode, PlanVersionID: running.PlanVersionID, Title: running.TaskTitle, Status: running.TaskStatus, Version: running.TaskVersion}
		dashboard.RunningSession = &RunningSessionView{Session: sessionView(running.StudySession), Task: task}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return dashboard, nil
}

func (s *Service) versionAndPlan(ctx context.Context, repo Repository, userID, versionID string, lock bool) (*model.PlanVersion, *model.Plan, error) {
	version, err := repo.PlanVersion(ctx, userID, versionID, lock)
	if err != nil {
		return nil, nil, err
	}
	plan, err := repo.Plan(ctx, userID, version.PlanID, false)
	if err != nil {
		return nil, nil, err
	}
	return version, plan, nil
}

func versionMode(ctx context.Context, repo Repository, userID, versionID string) string {
	version, err := repo.PlanVersion(ctx, userID, versionID, false)
	if err != nil {
		return ""
	}
	return version.Mode
}

func finishRunningForTask(ctx context.Context, repo Repository, userID, taskID string, now time.Time) error {
	session, err := repo.RunningSessionForTask(ctx, userID, taskID, true)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	duration := uint64(0)
	if now.After(session.StartedAt) {
		duration = uint64(now.Sub(session.StartedAt).Seconds())
	}
	return repo.UpdateSession(ctx, userID, session.ID, map[string]any{"status": model.SessionStatusFinished, "ended_at": now, "duration_seconds": duration, "running_slot": nil})
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
