package studyflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"studyflow/internal/model"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound           = errors.New("resource not found")
	ErrValidation         = errors.New("validation failed")
	ErrConflict           = errors.New("conflict")
	ErrVersionConflict    = errors.New("version conflict")
	ErrInvalidState       = errors.New("invalid state")
	ErrNoRequiredChildren = errors.New("goal has no required children")
	// ErrIdempotencyConflict is a sibling of ErrConflict, not a wrapper: the HTTP
	// layer switches on errors.Is, so it must stay distinguishable.
	ErrIdempotencyConflict = errors.New("idempotency key conflict")
)

type CompletionConfirmationError struct {
	Scope   string
	Summary any
}

func (e *CompletionConfirmationError) Error() string { return "completion confirmation required" }

type AncestorStateError struct{ Ancestors []string }

func (e *AncestorStateError) Error() string { return "ancestor goals must be active" }

// Title limits. MySQL counts characters for varchar, so these are character
// limits and have to be checked with utf8.RuneCountInString — a byte count would
// reject CJK titles the schema accepts. They also keep an oversized value from
// reaching MySQL, where strict mode turns it into error 1406 and the API would
// answer 500 instead of 400.
const (
	titleLimitGoal      = 160
	titleLimitPlan      = 160
	titleLimitMilestone = 160
	titleLimitTask      = 255
)

func titleTooLong(value string, limit int) bool {
	return utf8.RuneCountInString(value) > limit
}

type Service struct {
	db    *gorm.DB
	now   func() time.Time
	cache Cache
}

// NewService builds a service with caching switched off.
func NewService(db *gorm.DB) *Service {
	return &Service{db: db, now: time.Now, cache: noopCache{}}
}

// NewServiceWithCache builds a service that reads its aggregate queries through
// the given cache. Passing nil is the same as NewService.
func NewServiceWithCache(db *gorm.DB, cache Cache) *Service {
	if cache == nil {
		return NewService(db)
	}
	return &Service{db: db, now: time.Now, cache: cache}
}

type CreateGoalInput struct {
	ParentGoalID    *string `json:"parent_goal_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	SuccessCriteria string  `json:"success_criteria"`
	TargetDate      *string `json:"target_date"`
}

type UpdateGoalInput struct {
	Version         uint    `json:"version"`
	ParentGoalID    *string `json:"parent_goal_id"`
	ClearParent     bool    `json:"clear_parent"`
	Title           *string `json:"title"`
	Description     *string `json:"description"`
	SuccessCriteria *string `json:"success_criteria"`
	TargetDate      *string `json:"target_date"`
	ClearTargetDate bool    `json:"clear_target_date"`
}

type GoalNode struct {
	ID                           string                `json:"id"`
	ParentGoalID                 *string               `json:"parent_goal_id"`
	Title                        string                `json:"title"`
	Description                  string                `json:"description"`
	SuccessCriteria              string                `json:"success_criteria"`
	TargetDate                   *string               `json:"target_date"`
	Status                       string                `json:"status"`
	Version                      uint                  `json:"version"`
	AchievedAt                   *time.Time            `json:"achieved_at"`
	ExcludedFromParentCompletion bool                  `json:"excluded_from_parent_completion"`
	ReadyToComplete              bool                  `json:"ready_to_complete"`
	CompletionSummary            GoalCompletionSummary `json:"completion_summary"`
	Plans                        []PlanSummary         `json:"plans"`
	Children                     []*GoalNode           `json:"children"`
}

type GoalCompletionSummary struct {
	IncludedChildren   int `json:"included_children"`
	AchievedChildren   int `json:"achieved_children"`
	IncompleteChildren int `json:"incomplete_children"`
	ExcludedChildren   int `json:"excluded_children"`
}

type PlanCompletionSummary struct {
	HasActiveVersion bool `json:"has_active_version"`
	TotalTasks       int  `json:"total_tasks"`
	DoneTasks        int  `json:"done_tasks"`
	OpenTasks        int  `json:"open_tasks"`
}

type PlanSummary struct {
	ID                         string                `json:"id"`
	GoalID                     string                `json:"goal_id"`
	Title                      string                `json:"title"`
	Description                string                `json:"description"`
	Objective                  string                `json:"objective"`
	SuccessCriteria            string                `json:"success_criteria"`
	TargetDate                 *string               `json:"target_date"`
	Status                     string                `json:"status"`
	Revision                   uint                  `json:"revision"`
	AchievedAt                 *time.Time            `json:"achieved_at"`
	ExcludedFromGoalCompletion bool                  `json:"excluded_from_goal_completion"`
	CurrentMode                string                `json:"current_mode"`
	Source                     string                `json:"source"`
	ActiveVersionID            *string               `json:"active_version_id"`
	ActiveVersionNo            *uint                 `json:"active_version_no"`
	DraftVersionID             *string               `json:"draft_version_id"`
	DoneTasks                  int                   `json:"done_tasks"`
	TotalTasks                 int                   `json:"total_tasks"`
	CompletionSummary          PlanCompletionSummary `json:"completion_summary"`
}

type CreatePlanInput struct {
	Title                 string  `json:"title"`
	Description           string  `json:"description"`
	Objective             string  `json:"objective"`
	SuccessCriteria       string  `json:"success_criteria"`
	TargetDate            *string `json:"target_date"`
	Mode                  string  `json:"mode"`
	WeeklyCapacityMinutes uint    `json:"weekly_capacity_minutes"`
	StartDate             *string `json:"start_date"`
	EndDate               *string `json:"end_date"`
}

type UpdatePlanInput struct {
	Revision        uint    `json:"revision"`
	GoalID          *string `json:"goal_id"`
	Title           *string `json:"title"`
	Description     *string `json:"description"`
	Objective       *string `json:"objective"`
	SuccessCriteria *string `json:"success_criteria"`
	TargetDate      *string `json:"target_date"`
	ClearTargetDate bool    `json:"clear_target_date"`
}

type CompletionInput struct {
	AcknowledgeIncomplete bool `json:"acknowledge_incomplete"`
}
type AbandonInput struct {
	ParentEffect string `json:"parent_effect"`
}
type CompletionPolicyInput struct {
	ParentEffect string `json:"parent_effect"`
}

type PlanView struct {
	ID                         string                `json:"id"`
	GoalID                     string                `json:"goal_id"`
	ActiveVersionID            *string               `json:"active_version_id"`
	Title                      string                `json:"title"`
	Description                string                `json:"description"`
	Objective                  string                `json:"objective"`
	SuccessCriteria            string                `json:"success_criteria"`
	TargetDate                 *string               `json:"target_date"`
	Status                     string                `json:"status"`
	Revision                   uint                  `json:"revision"`
	AchievedAt                 *time.Time            `json:"achieved_at"`
	ExcludedFromGoalCompletion bool                  `json:"excluded_from_goal_completion"`
	CompletionSummary          PlanCompletionSummary `json:"completion_summary"`
	Source                     string                `json:"source"`
	CreatedAt                  time.Time             `json:"created_at"`
}

type VersionView struct {
	ID                    string          `json:"id"`
	VersionNo             uint            `json:"version_no"`
	Status                string          `json:"status"`
	Mode                  string          `json:"mode"`
	SourceVersionID       *string         `json:"source_version_id"`
	WeeklyCapacityMinutes uint            `json:"weekly_capacity_minutes"`
	StartDate             *string         `json:"start_date"`
	EndDate               *string         `json:"end_date"`
	StructureRevision     uint            `json:"structure_revision"`
	ActivatedAt           *time.Time      `json:"activated_at"`
	SupersededAt          *time.Time      `json:"superseded_at"`
	Milestones            []MilestoneView `json:"milestones"`
	UnassignedTasks       []TaskView      `json:"unassigned_tasks"`
}

type CreatePlanVersionInput struct {
	CreationMode          string  `json:"creation_mode"`
	SourceVersionID       *string `json:"source_version_id"`
	Mode                  string  `json:"mode"`
	WeeklyCapacityMinutes uint    `json:"weekly_capacity_minutes"`
	StartDate             *string `json:"start_date"`
	EndDate               *string `json:"end_date"`
}

type UpdatePlanVersionInput struct {
	Mode                  *string `json:"mode"`
	WeeklyCapacityMinutes *uint   `json:"weekly_capacity_minutes"`
	StartDate             *string `json:"start_date"`
	EndDate               *string `json:"end_date"`
	ClearStartDate        bool    `json:"clear_start_date"`
	ClearEndDate          bool    `json:"clear_end_date"`
}

type PlanDetail struct {
	Plan     PlanView      `json:"plan"`
	Versions []VersionView `json:"versions"`
}

type MilestoneView struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Outcome  string     `json:"outcome"`
	Position uint       `json:"position"`
	Tasks    []TaskView `json:"tasks"`
}

type TaskView struct {
	ID              string     `json:"id"`
	PlanID          string     `json:"plan_id,omitempty"`
	PlanTitle       string     `json:"plan_title,omitempty"`
	PlanMode        string     `json:"plan_mode,omitempty"`
	PlanVersionID   string     `json:"plan_version_id"`
	MilestoneID     *string    `json:"milestone_id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	EstimateMinutes uint       `json:"estimate_minutes"`
	ScheduledDate   *string    `json:"scheduled_date"`
	Position        uint       `json:"position"`
	Status          string     `json:"status"`
	Version         uint       `json:"version"`
	Source          string     `json:"source"`
	CompletedAt     *time.Time `json:"completed_at"`
	IsOverdue       bool       `json:"is_overdue"`
}

func (s *Service) CreateGoal(ctx context.Context, userID string, input CreateGoalInput) (*GoalNode, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: title is required", ErrValidation)
	}
	if titleTooLong(title, titleLimitGoal) {
		return nil, fmt.Errorf("%w: goal title too long", ErrValidation)
	}
	target, err := parseOptionalDate(input.TargetDate)
	if err != nil {
		return nil, err
	}
	goal := model.Goal{
		ID: uuid.NewString(), UserID: userID, ParentGoalID: input.ParentGoalID,
		Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description),
		SuccessCriteria: strings.TrimSpace(input.SuccessCriteria), TargetDate: target,
		Status: model.GoalStatusActive, Version: 1,
	}
	if input.ParentGoalID != nil {
		parent, err := s.getGoal(ctx, s.db, userID, *input.ParentGoalID, false)
		if err != nil {
			return nil, err
		}
		if parent.Status != model.GoalStatusActive {
			return nil, ErrInvalidState
		}
	}
	if err := s.db.WithContext(ctx).Create(&goal).Error; err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	node := goalNode(goal)
	return &node, nil
}

func (s *Service) UpdateGoal(ctx context.Context, userID, goalID string, input UpdateGoalInput) (*GoalNode, error) {
	var updated model.Goal
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		goal, err := s.getGoal(ctx, tx, userID, goalID, true)
		if err != nil {
			return err
		}
		if goal.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		if input.Version == 0 || goal.Version != input.Version {
			return ErrVersionConflict
		}
		changes := map[string]any{"version": gorm.Expr("version + 1")}
		if input.Title != nil {
			title := strings.TrimSpace(*input.Title)
			if title == "" {
				return fmt.Errorf("%w: title is required", ErrValidation)
			}
			if titleTooLong(title, titleLimitGoal) {
				return fmt.Errorf("%w: goal title too long", ErrValidation)
			}
			changes["title"] = title
		}
		if input.Description != nil {
			changes["description"] = strings.TrimSpace(*input.Description)
		}
		if input.SuccessCriteria != nil {
			changes["success_criteria"] = strings.TrimSpace(*input.SuccessCriteria)
		}
		if input.ClearTargetDate {
			changes["target_date"] = nil
		} else if input.TargetDate != nil {
			value, err := parseDate(*input.TargetDate)
			if err != nil {
				return err
			}
			changes["target_date"] = value
		}
		if input.ClearParent {
			changes["parent_goal_id"] = nil
		} else if input.ParentGoalID != nil {
			if *input.ParentGoalID == goalID {
				return fmt.Errorf("%w: goal cycle", ErrValidation)
			}
			if err := s.ensureNoGoalCycle(ctx, tx, userID, goalID, *input.ParentGoalID); err != nil {
				return err
			}
			parent, err := s.getGoal(ctx, tx, userID, *input.ParentGoalID, false)
			if err != nil {
				return err
			}
			if parent.Status != model.GoalStatusActive {
				return ErrInvalidState
			}
			changes["parent_goal_id"] = *input.ParentGoalID
		}
		result := tx.Model(&model.Goal{}).Where("id = ? AND user_id = ? AND version = ?", goalID, userID, input.Version).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVersionConflict
		}
		return tx.Where("id = ? AND user_id = ?", goalID, userID).First(&updated).Error
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	s.invalidate(ctx, userID)
	node := goalNode(updated)
	return &node, nil
}

func (s *Service) CompleteGoal(ctx context.Context, userID, goalID string, input CompletionInput) (*GoalNode, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		goal, err := s.getGoal(ctx, tx, userID, goalID, true)
		if err != nil {
			return err
		}
		if goal.Status == model.GoalStatusAchieved {
			return nil
		}
		if goal.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		summary, err := s.goalCompletionSummary(ctx, tx, userID, goalID)
		if err != nil {
			return err
		}
		if summary.IncludedChildren == 0 {
			return ErrNoRequiredChildren
		}
		if summary.IncompleteChildren > 0 && !input.AcknowledgeIncomplete {
			return &CompletionConfirmationError{Scope: "goal", Summary: summary}
		}
		now := s.now().UTC()
		return tx.Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID).Updates(map[string]any{"status": model.GoalStatusAchieved, "achieved_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.goalByID(ctx, userID, goalID)
}

func (s *Service) AbandonGoal(ctx context.Context, userID, goalID string, input AbandonInput) (*GoalNode, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		goal, err := s.getGoal(ctx, tx, userID, goalID, true)
		if err != nil {
			return err
		}
		if goal.Status == model.GoalStatusAbandoned {
			return nil
		}
		if goal.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		excluded := false
		if goal.ParentGoalID != nil {
			if input.ParentEffect != "block" && input.ParentEffect != "exclude" {
				return ErrValidation
			}
			excluded = input.ParentEffect == "exclude"
		}
		return tx.Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID).Updates(map[string]any{"status": model.GoalStatusAbandoned, "achieved_at": nil, "excluded_from_parent_completion": excluded, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.goalByID(ctx, userID, goalID)
}

func (s *Service) ReopenGoal(ctx context.Context, userID, goalID string) (*GoalNode, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		goal, err := s.getGoal(ctx, tx, userID, goalID, true)
		if err != nil {
			return err
		}
		if goal.Status == model.GoalStatusActive {
			return nil
		}
		ancestors, err := s.inactiveGoalAncestors(ctx, tx, userID, goal.ParentGoalID)
		if err != nil {
			return err
		}
		if len(ancestors) > 0 {
			return &AncestorStateError{Ancestors: ancestors}
		}
		return tx.Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID).Updates(map[string]any{"status": model.GoalStatusActive, "achieved_at": nil, "excluded_from_parent_completion": false, "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.goalByID(ctx, userID, goalID)
}

func (s *Service) UpdateGoalCompletionPolicy(ctx context.Context, userID, goalID string, input CompletionPolicyInput) (*GoalNode, error) {
	if input.ParentEffect != "block" && input.ParentEffect != "exclude" {
		return nil, ErrValidation
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		goal, err := s.getGoal(ctx, tx, userID, goalID, true)
		if err != nil {
			return err
		}
		if goal.ParentGoalID == nil || goal.Status != model.GoalStatusAbandoned {
			return ErrInvalidState
		}
		return tx.Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID).Updates(map[string]any{"excluded_from_parent_completion": input.ParentEffect == "exclude", "version": gorm.Expr("version + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.goalByID(ctx, userID, goalID)
}

// GoalTree returns the caller's goal tree, reading through the cache.
func (s *Service) GoalTree(ctx context.Context, userID string) ([]*GoalNode, error) {
	generation, known := s.cache.Generation(ctx, userID)
	key := goalTreeKey(userID, generation)
	if known {
		var cached []*GoalNode
		if s.cache.Read(ctx, key, &cached) {
			return cached, nil
		}
	}
	roots, err := s.loadGoalTree(ctx, userID)
	if err != nil {
		return nil, err
	}
	if known {
		s.cache.Write(ctx, key, roots)
	}
	return roots, nil
}

// loadGoalTree reads the tree straight from MySQL.
func (s *Service) loadGoalTree(ctx context.Context, userID string) ([]*GoalNode, error) {
	var goals []model.Goal
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at, id").Find(&goals).Error; err != nil {
		return nil, err
	}
	var plans []model.Plan
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at, id").Find(&plans).Error; err != nil {
		return nil, err
	}
	var versions []model.PlanVersion
	if err := s.db.WithContext(ctx).Where("user_id = ? AND status IN ?", userID, []string{model.PlanVersionActive, model.PlanVersionDraft}).Find(&versions).Error; err != nil {
		return nil, err
	}
	var tasks []model.Task
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&tasks).Error; err != nil {
		return nil, err
	}

	versionByID := make(map[string]model.PlanVersion, len(versions))
	draftByPlan := map[string]model.PlanVersion{}
	for _, version := range versions {
		versionByID[version.ID] = version
		if version.Status == model.PlanVersionDraft {
			draftByPlan[version.PlanID] = version
		}
	}
	taskCounts := map[string][2]int{}
	for _, task := range tasks {
		count := taskCounts[task.PlanVersionID]
		if task.Status != model.TaskStatusCanceled {
			count[1]++
		}
		if task.Status == model.TaskStatusDone {
			count[0]++
		}
		taskCounts[task.PlanVersionID] = count
	}
	plansByGoal := map[string][]PlanSummary{}
	for _, plan := range plans {
		summary := planSummary(plan)
		if plan.ActiveVersionID != nil {
			if version, ok := versionByID[*plan.ActiveVersionID]; ok {
				number := version.VersionNo
				summary.ActiveVersionNo = &number
				summary.CurrentMode = version.Mode
			}
			count := taskCounts[*plan.ActiveVersionID]
			summary.DoneTasks, summary.TotalTasks = count[0], count[1]
			summary.CompletionSummary = PlanCompletionSummary{HasActiveVersion: true, TotalTasks: count[1], DoneTasks: count[0], OpenTasks: count[1] - count[0]}
		}
		if version, ok := draftByPlan[plan.ID]; ok {
			id := version.ID
			summary.DraftVersionID = &id
			if summary.CurrentMode == "" {
				summary.CurrentMode = version.Mode
			}
		}
		plansByGoal[plan.GoalID] = append(plansByGoal[plan.GoalID], summary)
	}

	nodes := make(map[string]*GoalNode, len(goals))
	for _, goal := range goals {
		node := goalNode(goal)
		// A map lookup misses for goals without plans; assigning it would overwrite
		// the empty slice from goalNode with nil and serialize as JSON null.
		if goalPlans, ok := plansByGoal[goal.ID]; ok {
			node.Plans = goalPlans
		}
		nodes[goal.ID] = &node
	}
	roots := make([]*GoalNode, 0)
	for _, goal := range goals {
		node := nodes[goal.ID]
		if goal.ParentGoalID != nil && nodes[*goal.ParentGoalID] != nil {
			nodes[*goal.ParentGoalID].Children = append(nodes[*goal.ParentGoalID].Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	var evaluate func(*GoalNode)
	evaluate = func(node *GoalNode) {
		summary := GoalCompletionSummary{}
		for _, child := range node.Children {
			evaluate(child)
			if child.Status == model.GoalStatusAbandoned && child.ExcludedFromParentCompletion {
				summary.ExcludedChildren++
				continue
			}
			summary.IncludedChildren++
			if child.Status == model.GoalStatusAchieved {
				summary.AchievedChildren++
			} else {
				summary.IncompleteChildren++
			}
		}
		for _, plan := range node.Plans {
			if plan.Status == model.GoalStatusAbandoned && plan.ExcludedFromGoalCompletion {
				summary.ExcludedChildren++
				continue
			}
			summary.IncludedChildren++
			if plan.Status == model.GoalStatusAchieved {
				summary.AchievedChildren++
			} else {
				summary.IncompleteChildren++
			}
		}
		node.CompletionSummary = summary
		node.ReadyToComplete = node.Status == model.GoalStatusActive && summary.IncludedChildren > 0 && summary.IncompleteChildren == 0
	}
	for _, root := range roots {
		evaluate(root)
	}
	return roots, nil
}

// normalizePlanInput trims the free-text fields, validates the mode and the date
// range, and parses the optional start and end dates.
func normalizePlanInput(input CreatePlanInput) (CreatePlanInput, *time.Time, *time.Time, *time.Time, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	input.Objective = strings.TrimSpace(input.Objective)
	input.SuccessCriteria = strings.TrimSpace(input.SuccessCriteria)
	if input.Title == "" || input.Objective == "" || titleTooLong(input.Title, titleLimitPlan) || !validPlanMode(input.Mode) {
		return input, nil, nil, nil, ErrValidation
	}
	targetDate, err := parseOptionalDate(input.TargetDate)
	if err != nil {
		return input, nil, nil, nil, err
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil {
		return input, nil, nil, nil, err
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return input, nil, nil, nil, err
	}
	if startDate != nil && endDate != nil && endDate.Before(*startDate) {
		return input, nil, nil, nil, ErrValidation
	}
	return input, targetDate, startDate, endDate, nil
}

// insertPlanWithVersion writes a plan together with its first draft version. It
// performs no version bookkeeping, so a caller-owned transaction can use it to
// build a whole plan tree without nesting transactions.
func (s *Service) insertPlanWithVersion(ctx context.Context, tx *gorm.DB, userID, goalID string, plan model.Plan, version model.PlanVersion) error {
	goal, err := s.getGoal(ctx, tx, userID, goalID, false)
	if err != nil {
		return err
	}
	if goal.Status != model.GoalStatusActive {
		return ErrInvalidState
	}
	if err := tx.Create(&plan).Error; err != nil {
		return err
	}
	return tx.Create(&version).Error
}

func (s *Service) CreatePlan(ctx context.Context, userID, goalID string, input CreatePlanInput) (*PlanDetail, error) {
	input, targetDate, startDate, endDate, err := normalizePlanInput(input)
	if err != nil {
		return nil, err
	}
	plan := model.Plan{ID: uuid.NewString(), UserID: userID, GoalID: goalID, Title: input.Title, Description: input.Description, Objective: input.Objective, SuccessCriteria: input.SuccessCriteria, TargetDate: targetDate, Status: model.GoalStatusActive, Revision: 1, Source: model.SourceUser}
	version := model.PlanVersion{ID: uuid.NewString(), UserID: userID, PlanID: plan.ID, VersionNo: 1, Status: model.PlanVersionDraft, Mode: input.Mode, WeeklyCapacityMinutes: input.WeeklyCapacityMinutes, StartDate: startDate, EndDate: endDate, StructureRevision: 1}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.insertPlanWithVersion(ctx, tx, userID, goalID, plan, version)
	}); err != nil {
		return nil, mapNotFound(err)
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, plan.ID)
}

func (s *Service) UpdatePlan(ctx context.Context, userID, planID string, input UpdatePlanInput) (*PlanDetail, error) {
	if input.Revision == 0 {
		return nil, ErrValidation
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Revision != input.Revision {
			return ErrVersionConflict
		}
		if plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		changes := map[string]any{"revision": gorm.Expr("revision + 1")}
		if input.Title != nil {
			value := strings.TrimSpace(*input.Title)
			if value == "" || titleTooLong(value, titleLimitPlan) {
				return ErrValidation
			}
			changes["title"] = value
		}
		if input.Description != nil {
			changes["description"] = strings.TrimSpace(*input.Description)
		}
		if input.Objective != nil {
			value := strings.TrimSpace(*input.Objective)
			if value == "" {
				return ErrValidation
			}
			changes["objective"] = value
		}
		if input.SuccessCriteria != nil {
			changes["success_criteria"] = strings.TrimSpace(*input.SuccessCriteria)
		}
		if input.ClearTargetDate {
			changes["target_date"] = nil
		} else if input.TargetDate != nil {
			value, err := parseDate(*input.TargetDate)
			if err != nil {
				return err
			}
			changes["target_date"] = value
		}
		if input.GoalID != nil && *input.GoalID != plan.GoalID {
			goal, err := s.getGoal(ctx, tx, userID, *input.GoalID, false)
			if err != nil {
				return err
			}
			if goal.Status != model.GoalStatusActive {
				return ErrInvalidState
			}
			changes["goal_id"] = *input.GoalID
		}
		result := tx.Model(&model.Plan{}).Where("id = ? AND user_id = ? AND revision = ?", planID, userID, input.Revision).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVersionConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) CompletePlan(ctx context.Context, userID, planID string, input CompletionInput) (*PlanDetail, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Status == model.GoalStatusAchieved {
			return nil
		}
		if plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		summary, err := s.planCompletionSummary(ctx, tx, userID, plan)
		if err != nil {
			return err
		}
		if (!summary.HasActiveVersion || summary.OpenTasks > 0) && !input.AcknowledgeIncomplete {
			return &CompletionConfirmationError{Scope: "plan", Summary: summary}
		}
		if plan.ActiveVersionID != nil {
			var running int64
			if err := tx.Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Where("s.user_id = ? AND s.status = ? AND t.plan_version_id = ?", userID, model.SessionStatusRunning, *plan.ActiveVersionID).Count(&running).Error; err != nil {
				return err
			}
			if running > 0 {
				return ErrConflict
			}
		}
		now := s.now().UTC()
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID).Updates(map[string]any{"status": model.GoalStatusAchieved, "achieved_at": now, "revision": gorm.Expr("revision + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) AbandonPlan(ctx context.Context, userID, planID string, input AbandonInput) (*PlanDetail, error) {
	if input.ParentEffect != "block" && input.ParentEffect != "exclude" {
		return nil, ErrValidation
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Status == model.GoalStatusAbandoned {
			return nil
		}
		if plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		var running int64
		if err := tx.Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Where("s.user_id = ? AND s.status = ? AND pv.plan_id = ?", userID, model.SessionStatusRunning, planID).Count(&running).Error; err != nil {
			return err
		}
		if running > 0 {
			return ErrConflict
		}
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID).Updates(map[string]any{"status": model.GoalStatusAbandoned, "achieved_at": nil, "excluded_from_goal_completion": input.ParentEffect == "exclude", "revision": gorm.Expr("revision + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) ReopenPlan(ctx context.Context, userID, planID string) (*PlanDetail, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Status == model.GoalStatusActive {
			return nil
		}
		ancestors, err := s.inactiveGoalAncestors(ctx, tx, userID, &plan.GoalID)
		if err != nil {
			return err
		}
		if len(ancestors) > 0 {
			return &AncestorStateError{Ancestors: ancestors}
		}
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID).Updates(map[string]any{"status": model.GoalStatusActive, "achieved_at": nil, "excluded_from_goal_completion": false, "revision": gorm.Expr("revision + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) UpdatePlanCompletionPolicy(ctx context.Context, userID, planID string, input CompletionPolicyInput) (*PlanDetail, error) {
	if input.ParentEffect != "block" && input.ParentEffect != "exclude" {
		return nil, ErrValidation
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Status != model.GoalStatusAbandoned {
			return ErrInvalidState
		}
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID).Updates(map[string]any{"excluded_from_goal_completion": input.ParentEffect == "exclude", "revision": gorm.Expr("revision + 1")}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) ListPlans(ctx context.Context, userID, goalID, mode, status string) ([]PlanSummary, error) {
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if goalID != "" {
		query = query.Where("goal_id = ?", goalID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var plans []model.Plan
	if err := query.Order("created_at DESC").Find(&plans).Error; err != nil {
		return nil, err
	}
	result := make([]PlanSummary, 0, len(plans))
	for _, plan := range plans {
		summary := planSummary(plan)
		if plan.ActiveVersionID != nil {
			var version model.PlanVersion
			if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&version).Error; err == nil {
				n := version.VersionNo
				summary.ActiveVersionNo = &n
				summary.CurrentMode = version.Mode
			}
			var total, done int64
			if err := s.db.WithContext(ctx).Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ? AND status <> ?", *plan.ActiveVersionID, userID, model.TaskStatusCanceled).Count(&total).Error; err != nil {
				return nil, err
			}
			if err := s.db.WithContext(ctx).Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ? AND status = ?", *plan.ActiveVersionID, userID, model.TaskStatusDone).Count(&done).Error; err != nil {
				return nil, err
			}
			summary.TotalTasks, summary.DoneTasks = int(total), int(done)
			summary.CompletionSummary = PlanCompletionSummary{HasActiveVersion: true, TotalTasks: int(total), DoneTasks: int(done), OpenTasks: int(total - done)}
		}
		var draft model.PlanVersion
		if err := s.db.WithContext(ctx).Where("plan_id = ? AND user_id = ? AND status = ?", plan.ID, userID, model.PlanVersionDraft).Order("version_no DESC").First(&draft).Error; err == nil {
			id := draft.ID
			summary.DraftVersionID = &id
			if summary.CurrentMode == "" {
				summary.CurrentMode = draft.Mode
			}
		}
		if mode == "" || summary.CurrentMode == mode {
			result = append(result, summary)
		}
	}
	return result, nil
}

func (s *Service) GetPlan(ctx context.Context, userID, planID string) (*PlanDetail, error) {
	var plan model.Plan
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
		return nil, mapNotFound(err)
	}
	var versions []model.PlanVersion
	if err := s.db.WithContext(ctx).Where("plan_id = ? AND user_id = ?", planID, userID).Order("version_no DESC").Find(&versions).Error; err != nil {
		return nil, err
	}
	completion, err := s.planCompletionSummary(ctx, s.db, userID, plan)
	if err != nil {
		return nil, err
	}
	detail := &PlanDetail{Plan: planView(plan, completion)}
	for _, version := range versions {
		view, err := s.versionView(ctx, userID, version)
		if err != nil {
			return nil, err
		}
		detail.Versions = append(detail.Versions, view)
	}
	return detail, nil
}

func (s *Service) CreatePlanVersion(ctx context.Context, userID, planID string, input CreatePlanVersionInput) (*VersionView, error) {
	var created model.PlanVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		var existing int64
		if err := tx.Model(&model.PlanVersion{}).Where("plan_id = ? AND user_id = ? AND status = ?", planID, userID, model.PlanVersionDraft).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrConflict
		}
		var maxVersion uint
		if err := tx.Model(&model.PlanVersion{}).Where("plan_id = ? AND user_id = ?", planID, userID).Select("COALESCE(MAX(version_no), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		created = model.PlanVersion{ID: uuid.NewString(), UserID: userID, PlanID: planID, VersionNo: maxVersion + 1, Status: model.PlanVersionDraft, StructureRevision: 1}
		var source *model.PlanVersion
		switch input.CreationMode {
		case "blank":
			if !validPlanMode(input.Mode) {
				return ErrValidation
			}
			startDate, err := parseOptionalDate(input.StartDate)
			if err != nil {
				return err
			}
			endDate, err := parseOptionalDate(input.EndDate)
			if err != nil {
				return err
			}
			if startDate != nil && endDate != nil && endDate.Before(*startDate) {
				return ErrValidation
			}
			created.Mode, created.WeeklyCapacityMinutes, created.StartDate, created.EndDate = input.Mode, input.WeeklyCapacityMinutes, startDate, endDate
		case "copy":
			if input.SourceVersionID == nil || strings.TrimSpace(*input.SourceVersionID) == "" {
				return ErrValidation
			}
			var value model.PlanVersion
			if err := tx.Where("id = ? AND plan_id = ? AND user_id = ?", *input.SourceVersionID, planID, userID).First(&value).Error; err != nil {
				return mapNotFound(err)
			}
			source = &value
			sourceID := value.ID
			created.SourceVersionID = &sourceID
			created.Mode, created.WeeklyCapacityMinutes, created.StartDate, created.EndDate = value.Mode, value.WeeklyCapacityMinutes, value.StartDate, value.EndDate
		default:
			return ErrValidation
		}
		if err := tx.Create(&created).Error; err != nil {
			// Two clones racing both compute version_no = base + 1; the loser hits
			// uk_plan_versions_number, which is a conflict and not a server error.
			if isDuplicate(err) {
				return ErrConflict
			}
			return err
		}
		if source == nil {
			return nil
		}
		var milestones []model.Milestone
		if err := tx.Where("plan_version_id = ? AND user_id = ?", source.ID, userID).Order("position").Find(&milestones).Error; err != nil {
			return err
		}
		milestoneMap := map[string]string{}
		for _, item := range milestones {
			copy := model.Milestone{ID: uuid.NewString(), UserID: userID, PlanVersionID: created.ID, Title: item.Title, Outcome: item.Outcome, Position: item.Position}
			if err := tx.Create(&copy).Error; err != nil {
				return err
			}
			milestoneMap[item.ID] = copy.ID
		}
		var tasks []model.Task
		if err := tx.Where("plan_version_id = ? AND user_id = ?", source.ID, userID).Order("position").Find(&tasks).Error; err != nil {
			return err
		}
		for _, item := range tasks {
			var milestoneID *string
			if item.MilestoneID != nil {
				if mapped := milestoneMap[*item.MilestoneID]; mapped != "" {
					milestoneID = &mapped
				}
			}
			sourceID := item.ID
			copy := model.Task{ID: uuid.NewString(), UserID: userID, PlanVersionID: created.ID, MilestoneID: milestoneID, CopiedFromTaskID: &sourceID, Title: item.Title, Description: item.Description, EstimateMinutes: item.EstimateMinutes, ScheduledDate: item.ScheduledDate, Position: item.Position, Status: item.Status, Version: 1, Source: item.Source}
			if err := tx.Create(&copy).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view, err := s.versionView(ctx, userID, created)
	return &view, err
}

func (s *Service) UpdatePlanVersion(ctx context.Context, userID, versionID string, input UpdatePlanVersionInput) (*VersionView, error) {
	var updated model.PlanVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, plan, err := s.versionAndPlan(ctx, tx, userID, versionID, true)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		changes := map[string]any{}
		if input.Mode != nil {
			if !validPlanMode(*input.Mode) {
				return ErrValidation
			}
			if *input.Mode == model.PlanModeSequence {
				var scheduled int64
				if err := tx.Model(&model.Task{}).Where("user_id = ? AND plan_version_id = ? AND scheduled_date IS NOT NULL", userID, versionID).Count(&scheduled).Error; err != nil {
					return err
				}
				if scheduled > 0 {
					return ErrValidation
				}
			}
			changes["mode"] = *input.Mode
		}
		if input.WeeklyCapacityMinutes != nil {
			changes["weekly_capacity_minutes"] = *input.WeeklyCapacityMinutes
		}
		if input.ClearStartDate {
			changes["start_date"] = nil
		} else if input.StartDate != nil {
			value, err := parseDate(*input.StartDate)
			if err != nil {
				return err
			}
			changes["start_date"] = value
		}
		if input.ClearEndDate {
			changes["end_date"] = nil
		} else if input.EndDate != nil {
			value, err := parseDate(*input.EndDate)
			if err != nil {
				return err
			}
			changes["end_date"] = value
		}
		start, end := version.StartDate, version.EndDate
		if value, ok := changes["start_date"]; ok {
			if value == nil {
				start = nil
			} else {
				parsed := value.(*time.Time)
				start = parsed
			}
		}
		if value, ok := changes["end_date"]; ok {
			if value == nil {
				end = nil
			} else {
				parsed := value.(*time.Time)
				end = parsed
			}
		}
		if start != nil && end != nil && end.Before(*start) {
			return ErrValidation
		}
		if len(changes) > 0 {
			if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", versionID, userID).Updates(changes).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND user_id = ?", versionID, userID).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view, err := s.versionView(ctx, userID, updated)
	return &view, err
}

func (s *Service) CancelPlanVersion(ctx context.Context, userID, versionID string) (*VersionView, error) {
	var updated model.PlanVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		version, plan, err := s.versionAndPlan(ctx, tx, userID, versionID, true)
		if err != nil {
			return err
		}
		if version.Status != model.PlanVersionDraft || plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", versionID, userID).Update("status", model.PlanVersionCanceled).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND user_id = ?", versionID, userID).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	view, err := s.versionView(ctx, userID, updated)
	return &view, err
}

func (s *Service) ActivatePlanVersion(ctx context.Context, userID, versionID string) (*PlanDetail, error) {
	var planID string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version model.PlanVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", versionID, userID).First(&version).Error; err != nil {
			return mapNotFound(err)
		}
		if version.Status == model.PlanVersionCanceled {
			return ErrInvalidState
		}
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", version.PlanID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		planID = plan.ID
		if plan.Status != model.GoalStatusActive {
			return ErrInvalidState
		}
		if plan.ActiveVersionID != nil && *plan.ActiveVersionID == version.ID {
			if version.Status == model.PlanVersionActive {
				return nil
			}
			return ErrInvalidState
		}
		if version.Status != model.PlanVersionDraft && version.Status != model.PlanVersionSuperseded {
			return ErrInvalidState
		}
		if plan.ActiveVersionID != nil {
			var active model.PlanVersion
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&active).Error; err != nil {
				return mapNotFound(err)
			}
			var running int64
			if err := tx.Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Where("s.user_id = ? AND s.status = ? AND t.plan_version_id = ?", userID, model.SessionStatusRunning, active.ID).Count(&running).Error; err != nil {
				return err
			}
			if running > 0 {
				return ErrConflict
			}
			now := s.now().UTC()
			if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", active.ID, userID).Updates(map[string]any{"status": model.PlanVersionSuperseded, "superseded_at": now}).Error; err != nil {
				return err
			}
		}
		now := s.now().UTC()
		if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", version.ID, userID).Updates(map[string]any{"status": model.PlanVersionActive, "activated_at": now, "superseded_at": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", plan.ID, userID).Update("active_version_id", version.ID).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidate(ctx, userID)
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) versionView(ctx context.Context, userID string, version model.PlanVersion) (VersionView, error) {
	view := VersionView{ID: version.ID, VersionNo: version.VersionNo, Status: version.Status, Mode: version.Mode, SourceVersionID: version.SourceVersionID, WeeklyCapacityMinutes: version.WeeklyCapacityMinutes, StartDate: formatDatePtr(version.StartDate), EndDate: formatDatePtr(version.EndDate), StructureRevision: version.StructureRevision, ActivatedAt: version.ActivatedAt, SupersededAt: version.SupersededAt, Milestones: []MilestoneView{}, UnassignedTasks: []TaskView{}}
	var milestones []model.Milestone
	if err := s.db.WithContext(ctx).Where("plan_version_id = ? AND user_id = ?", version.ID, userID).Order("position, id").Find(&milestones).Error; err != nil {
		return view, err
	}
	var tasks []model.Task
	if err := s.db.WithContext(ctx).Where("plan_version_id = ? AND user_id = ?", version.ID, userID).Order("position, id").Find(&tasks).Error; err != nil {
		return view, err
	}
	byMilestone := map[string][]TaskView{}
	for _, task := range tasks {
		item := taskView(task, "", "", "", time.Time{})
		if task.MilestoneID == nil {
			view.UnassignedTasks = append(view.UnassignedTasks, item)
		} else {
			byMilestone[*task.MilestoneID] = append(byMilestone[*task.MilestoneID], item)
		}
	}
	for _, milestone := range milestones {
		// byMilestone misses for milestones without tasks; the frontend treats the
		// field as a non-null array.
		milestoneTasks := byMilestone[milestone.ID]
		if milestoneTasks == nil {
			milestoneTasks = []TaskView{}
		}
		view.Milestones = append(view.Milestones, MilestoneView{ID: milestone.ID, Title: milestone.Title, Outcome: milestone.Outcome, Position: milestone.Position, Tasks: milestoneTasks})
	}
	return view, nil
}

func (s *Service) getGoal(ctx context.Context, db *gorm.DB, userID, goalID string, lock bool) (*model.Goal, error) {
	query := db.WithContext(ctx)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var goal model.Goal
	if err := query.Where("id = ? AND user_id = ?", goalID, userID).First(&goal).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &goal, nil
}

func (s *Service) ensureNoGoalCycle(ctx context.Context, tx *gorm.DB, userID, goalID, newParentID string) error {
	var goals []model.Goal
	if err := tx.WithContext(ctx).Where("user_id = ?", userID).Find(&goals).Error; err != nil {
		return err
	}
	parents := map[string]*string{}
	for i := range goals {
		parents[goals[i].ID] = goals[i].ParentGoalID
	}
	if _, ok := parents[newParentID]; !ok {
		return ErrNotFound
	}
	current := newParentID
	seen := map[string]bool{}
	for current != "" {
		if current == goalID {
			return fmt.Errorf("%w: goal cycle", ErrValidation)
		}
		if seen[current] {
			return fmt.Errorf("%w: existing goal cycle", ErrValidation)
		}
		seen[current] = true
		parent := parents[current]
		if parent == nil {
			break
		}
		current = *parent
	}
	return nil
}

func (s *Service) goalCompletionSummary(ctx context.Context, db *gorm.DB, userID, goalID string) (GoalCompletionSummary, error) {
	var goals []model.Goal
	if err := db.WithContext(ctx).Where("user_id = ? AND parent_goal_id = ?", userID, goalID).Find(&goals).Error; err != nil {
		return GoalCompletionSummary{}, err
	}
	var plans []model.Plan
	if err := db.WithContext(ctx).Where("user_id = ? AND goal_id = ?", userID, goalID).Find(&plans).Error; err != nil {
		return GoalCompletionSummary{}, err
	}
	summary := GoalCompletionSummary{}
	for _, goal := range goals {
		if goal.Status == model.GoalStatusAbandoned && goal.ExcludedFromParentCompletion {
			summary.ExcludedChildren++
			continue
		}
		summary.IncludedChildren++
		if goal.Status == model.GoalStatusAchieved {
			summary.AchievedChildren++
		} else {
			summary.IncompleteChildren++
		}
	}
	for _, plan := range plans {
		if plan.Status == model.GoalStatusAbandoned && plan.ExcludedFromGoalCompletion {
			summary.ExcludedChildren++
			continue
		}
		summary.IncludedChildren++
		if plan.Status == model.GoalStatusAchieved {
			summary.AchievedChildren++
		} else {
			summary.IncompleteChildren++
		}
	}
	return summary, nil
}

func (s *Service) planCompletionSummary(ctx context.Context, db *gorm.DB, userID string, plan model.Plan) (PlanCompletionSummary, error) {
	summary := PlanCompletionSummary{HasActiveVersion: plan.ActiveVersionID != nil}
	if plan.ActiveVersionID == nil {
		return summary, nil
	}
	var total, done int64
	if err := db.WithContext(ctx).Model(&model.Task{}).Where("user_id = ? AND plan_version_id = ? AND status <> ?", userID, *plan.ActiveVersionID, model.TaskStatusCanceled).Count(&total).Error; err != nil {
		return summary, err
	}
	if err := db.WithContext(ctx).Model(&model.Task{}).Where("user_id = ? AND plan_version_id = ? AND status = ?", userID, *plan.ActiveVersionID, model.TaskStatusDone).Count(&done).Error; err != nil {
		return summary, err
	}
	summary.TotalTasks, summary.DoneTasks, summary.OpenTasks = int(total), int(done), int(total-done)
	return summary, nil
}

func (s *Service) inactiveGoalAncestors(ctx context.Context, db *gorm.DB, userID string, startID *string) ([]string, error) {
	result := []string{}
	current := startID
	seen := map[string]bool{}
	for current != nil && *current != "" {
		if seen[*current] {
			return nil, ErrValidation
		}
		seen[*current] = true
		goal, err := s.getGoal(ctx, db, userID, *current, false)
		if err != nil {
			return nil, err
		}
		if goal.Status != model.GoalStatusActive {
			result = append(result, goal.ID)
		}
		current = goal.ParentGoalID
	}
	return result, nil
}

func (s *Service) goalByID(ctx context.Context, userID, goalID string) (*GoalNode, error) {
	roots, err := s.GoalTree(ctx, userID)
	if err != nil {
		return nil, err
	}
	var find func([]*GoalNode) *GoalNode
	find = func(nodes []*GoalNode) *GoalNode {
		for _, node := range nodes {
			if node.ID == goalID {
				return node
			}
			if found := find(node.Children); found != nil {
				return found
			}
		}
		return nil
	}
	if found := find(roots); found != nil {
		return found, nil
	}
	return nil, ErrNotFound
}

func planSummary(plan model.Plan) PlanSummary {
	return PlanSummary{ID: plan.ID, GoalID: plan.GoalID, Title: plan.Title, Description: plan.Description, Objective: plan.Objective, SuccessCriteria: plan.SuccessCriteria, TargetDate: formatDatePtr(plan.TargetDate), Status: plan.Status, Revision: plan.Revision, AchievedAt: plan.AchievedAt, ExcludedFromGoalCompletion: plan.ExcludedFromGoalCompletion, Source: plan.Source, ActiveVersionID: plan.ActiveVersionID}
}

func planView(plan model.Plan, completion PlanCompletionSummary) PlanView {
	return PlanView{ID: plan.ID, GoalID: plan.GoalID, ActiveVersionID: plan.ActiveVersionID, Title: plan.Title, Description: plan.Description, Objective: plan.Objective, SuccessCriteria: plan.SuccessCriteria, TargetDate: formatDatePtr(plan.TargetDate), Status: plan.Status, Revision: plan.Revision, AchievedAt: plan.AchievedAt, ExcludedFromGoalCompletion: plan.ExcludedFromGoalCompletion, CompletionSummary: completion, Source: plan.Source, CreatedAt: plan.CreatedAt}
}

func validPlanMode(value string) bool {
	return value == model.PlanModeCalendar || value == model.PlanModeSequence
}

func goalNode(goal model.Goal) GoalNode {
	return GoalNode{ID: goal.ID, ParentGoalID: goal.ParentGoalID, Title: goal.Title, Description: goal.Description, SuccessCriteria: goal.SuccessCriteria, TargetDate: formatDatePtr(goal.TargetDate), Status: goal.Status, Version: goal.Version, AchievedAt: goal.AchievedAt, ExcludedFromParentCompletion: goal.ExcludedFromParentCompletion, Plans: []PlanSummary{}, Children: []*GoalNode{}}
}

func parseDate(value string) (*time.Time, error) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid date", ErrValidation)
	}
	return &parsed, nil
}

func parseOptionalDate(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	return parseDate(*value)
}

func formatDatePtr(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.Format("2006-01-02")
	return &formatted
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func isDuplicate(err error) bool {
	var value *mysql.MySQLError
	return errors.As(err, &value) && value.Number == 1062
}

func taskView(task model.Task, planID, planTitle, planMode string, today time.Time) TaskView {
	item := TaskView{ID: task.ID, PlanID: planID, PlanTitle: planTitle, PlanMode: planMode, PlanVersionID: task.PlanVersionID, MilestoneID: task.MilestoneID, Title: task.Title, Description: task.Description, EstimateMinutes: task.EstimateMinutes, ScheduledDate: formatDatePtr(task.ScheduledDate), Position: task.Position, Status: task.Status, Version: task.Version, Source: task.Source, CompletedAt: task.CompletedAt}
	if !today.IsZero() && task.ScheduledDate != nil && task.ScheduledDate.Before(today) && task.Status != model.TaskStatusDone && task.Status != model.TaskStatusCanceled {
		item.IsOverdue = true
	}
	return item
}

func sortTaskViews(items []TaskView) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ScheduledDate == nil {
			return false
		}
		if items[j].ScheduledDate == nil {
			return true
		}
		if *items[i].ScheduledDate == *items[j].ScheduledDate {
			return items[i].Position < items[j].Position
		}
		return *items[i].ScheduledDate < *items[j].ScheduledDate
	})
}
