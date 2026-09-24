package studyflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"studyflow/internal/model"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound            = errors.New("resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrConflict            = errors.New("conflict")
	ErrVersionConflict     = errors.New("version conflict")
	ErrPlanVersionConflict = errors.New("plan version conflict")
	ErrInvalidState        = errors.New("invalid state")
	// ErrIdempotencyConflict is a sibling of ErrConflict, not a wrapper: the HTTP
	// layer switches on errors.Is, so it must stay distinguishable.
	ErrIdempotencyConflict = errors.New("idempotency key conflict")
)

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func NewService(db *gorm.DB) *Service { return &Service{db: db, now: time.Now} }

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
	ID              string        `json:"id"`
	ParentGoalID    *string       `json:"parent_goal_id"`
	Title           string        `json:"title"`
	Description     string        `json:"description"`
	SuccessCriteria string        `json:"success_criteria"`
	TargetDate      *string       `json:"target_date"`
	Status          string        `json:"status"`
	Version         uint          `json:"version"`
	ReadyToComplete bool          `json:"ready_to_complete"`
	Plans           []PlanSummary `json:"plans"`
	Children        []*GoalNode   `json:"children"`
}

type PlanSummary struct {
	ID              string  `json:"id"`
	GoalID          string  `json:"goal_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	Mode            string  `json:"mode"`
	ActiveVersionID *string `json:"active_version_id"`
	ActiveVersionNo *uint   `json:"active_version_no"`
	DraftVersionID  *string `json:"draft_version_id"`
	DoneTasks       int     `json:"done_tasks"`
	TotalTasks      int     `json:"total_tasks"`
}

type CreatePlanInput struct {
	Title                 string  `json:"title"`
	Description           string  `json:"description"`
	Mode                  string  `json:"mode"`
	WeeklyCapacityMinutes uint    `json:"weekly_capacity_minutes"`
	StartDate             *string `json:"start_date"`
	EndDate               *string `json:"end_date"`
}

type UpdatePlanInput struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

type PlanView struct {
	ID              string    `json:"id"`
	GoalID          string    `json:"goal_id"`
	ActiveVersionID *string   `json:"active_version_id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Mode            string    `json:"mode"`
	CreatedAt       time.Time `json:"created_at"`
}

type VersionView struct {
	ID                    string          `json:"id"`
	VersionNo             uint            `json:"version_no"`
	Status                string          `json:"status"`
	WeeklyCapacityMinutes uint            `json:"weekly_capacity_minutes"`
	StartDate             *string         `json:"start_date"`
	EndDate               *string         `json:"end_date"`
	StructureRevision     uint            `json:"structure_revision"`
	ActivatedAt           *time.Time      `json:"activated_at"`
	SupersededAt          *time.Time      `json:"superseded_at"`
	Milestones            []MilestoneView `json:"milestones"`
	UnassignedTasks       []TaskView      `json:"unassigned_tasks"`
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
	CompletedAt     *time.Time `json:"completed_at"`
	IsOverdue       bool       `json:"is_overdue"`
}

func (s *Service) CreateGoal(ctx context.Context, userID string, input CreateGoalInput) (*GoalNode, error) {
	if strings.TrimSpace(input.Title) == "" {
		return nil, fmt.Errorf("%w: title is required", ErrValidation)
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
		if _, err := s.getGoal(ctx, s.db, userID, *input.ParentGoalID, false); err != nil {
			return nil, err
		}
	}
	if err := s.db.WithContext(ctx).Create(&goal).Error; err != nil {
		return nil, err
	}
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
		if input.Version == 0 || goal.Version != input.Version {
			return ErrVersionConflict
		}
		changes := map[string]any{"version": gorm.Expr("version + 1")}
		if input.Title != nil {
			title := strings.TrimSpace(*input.Title)
			if title == "" {
				return fmt.Errorf("%w: title is required", ErrValidation)
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
	node := goalNode(updated)
	return &node, nil
}

func (s *Service) SetGoalStatus(ctx context.Context, userID, goalID, status string) error {
	if status != model.GoalStatusAchieved && status != model.GoalStatusAbandoned {
		return ErrValidation
	}
	result := s.db.WithContext(ctx).Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID).
		Updates(map[string]any{"status": status, "version": gorm.Expr("version + 1")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) GoalTree(ctx context.Context, userID string) ([]*GoalNode, error) {
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
		summary := PlanSummary{ID: plan.ID, GoalID: plan.GoalID, Title: plan.Title, Description: plan.Description, Mode: plan.Mode, ActiveVersionID: plan.ActiveVersionID}
		if plan.ActiveVersionID != nil {
			if version, ok := versionByID[*plan.ActiveVersionID]; ok {
				number := version.VersionNo
				summary.ActiveVersionNo = &number
			}
			count := taskCounts[*plan.ActiveVersionID]
			summary.DoneTasks, summary.TotalTasks = count[0], count[1]
		}
		if version, ok := draftByPlan[plan.ID]; ok {
			id := version.ID
			summary.DraftVersionID = &id
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
	var evaluate func(*GoalNode) bool
	evaluate = func(node *GoalNode) bool {
		hasWork, ready := len(node.Children)+len(node.Plans) > 0, true
		for _, child := range node.Children {
			evaluate(child)
			if child.Status != model.GoalStatusAchieved {
				ready = false
			}
		}
		for _, plan := range node.Plans {
			if plan.ActiveVersionID == nil || plan.TotalTasks == 0 || plan.DoneTasks != plan.TotalTasks {
				ready = false
			}
		}
		node.ReadyToComplete = node.Status == model.GoalStatusActive && hasWork && ready
		return node.ReadyToComplete
	}
	for _, root := range roots {
		evaluate(root)
	}
	return roots, nil
}

// normalizePlanInput trims the free-text fields, validates the mode and the date
// range, and parses the optional start and end dates.
func normalizePlanInput(input CreatePlanInput) (CreatePlanInput, *time.Time, *time.Time, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)
	if input.Title == "" || (input.Mode != model.PlanModeCalendar && input.Mode != model.PlanModeSequence) {
		return input, nil, nil, ErrValidation
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil {
		return input, nil, nil, err
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return input, nil, nil, err
	}
	if startDate != nil && endDate != nil && endDate.Before(*startDate) {
		return input, nil, nil, ErrValidation
	}
	return input, startDate, endDate, nil
}

// insertPlanWithVersion writes a plan together with its first draft version. It
// performs no version bookkeeping, so a caller-owned transaction can use it to
// build a whole plan tree without nesting transactions.
func (s *Service) insertPlanWithVersion(ctx context.Context, tx *gorm.DB, userID, goalID string, plan model.Plan, version model.PlanVersion) error {
	if _, err := s.getGoal(ctx, tx, userID, goalID, false); err != nil {
		return err
	}
	if err := tx.Create(&plan).Error; err != nil {
		return err
	}
	return tx.Create(&version).Error
}

func (s *Service) CreatePlan(ctx context.Context, userID, goalID string, input CreatePlanInput) (*PlanDetail, error) {
	input, startDate, endDate, err := normalizePlanInput(input)
	if err != nil {
		return nil, err
	}
	plan := model.Plan{ID: uuid.NewString(), UserID: userID, GoalID: goalID, Title: input.Title, Description: input.Description, Mode: input.Mode}
	version := model.PlanVersion{ID: uuid.NewString(), UserID: userID, PlanID: plan.ID, VersionNo: 1, Status: model.PlanVersionDraft, WeeklyCapacityMinutes: input.WeeklyCapacityMinutes, StartDate: startDate, EndDate: endDate, StructureRevision: 1}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.insertPlanWithVersion(ctx, tx, userID, goalID, plan, version)
	}); err != nil {
		return nil, mapNotFound(err)
	}
	return s.GetPlan(ctx, userID, plan.ID)
}

func (s *Service) UpdatePlan(ctx context.Context, userID, planID string, input UpdatePlanInput) (*PlanDetail, error) {
	changes := map[string]any{}
	if input.Title != nil {
		value := strings.TrimSpace(*input.Title)
		if value == "" {
			return nil, ErrValidation
		}
		changes["title"] = value
	}
	if input.Description != nil {
		changes["description"] = strings.TrimSpace(*input.Description)
	}
	if len(changes) == 0 {
		return s.GetPlan(ctx, userID, planID)
	}
	result := s.db.WithContext(ctx).Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID).Updates(changes)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrNotFound
	}
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) ListPlans(ctx context.Context, userID, goalID, mode string) ([]PlanSummary, error) {
	query := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if goalID != "" {
		query = query.Where("goal_id = ?", goalID)
	}
	if mode != "" {
		query = query.Where("mode = ?", mode)
	}
	var plans []model.Plan
	if err := query.Order("created_at DESC").Find(&plans).Error; err != nil {
		return nil, err
	}
	result := make([]PlanSummary, 0, len(plans))
	for _, plan := range plans {
		summary := PlanSummary{ID: plan.ID, GoalID: plan.GoalID, Title: plan.Title, Description: plan.Description, Mode: plan.Mode, ActiveVersionID: plan.ActiveVersionID}
		if plan.ActiveVersionID != nil {
			var version model.PlanVersion
			if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&version).Error; err == nil {
				n := version.VersionNo
				summary.ActiveVersionNo = &n
			}
			var total, done int64
			s.db.WithContext(ctx).Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ? AND status <> ?", *plan.ActiveVersionID, userID, model.TaskStatusCanceled).Count(&total)
			s.db.WithContext(ctx).Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ? AND status = ?", *plan.ActiveVersionID, userID, model.TaskStatusDone).Count(&done)
			summary.TotalTasks, summary.DoneTasks = int(total), int(done)
		}
		var draft model.PlanVersion
		if err := s.db.WithContext(ctx).Where("plan_id = ? AND user_id = ? AND status = ?", plan.ID, userID, model.PlanVersionDraft).Order("version_no DESC").First(&draft).Error; err == nil {
			id := draft.ID
			summary.DraftVersionID = &id
		}
		result = append(result, summary)
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
	detail := &PlanDetail{Plan: PlanView{ID: plan.ID, GoalID: plan.GoalID, ActiveVersionID: plan.ActiveVersionID, Title: plan.Title, Description: plan.Description, Mode: plan.Mode, CreatedAt: plan.CreatedAt}}
	for _, version := range versions {
		view, err := s.versionView(ctx, userID, version)
		if err != nil {
			return nil, err
		}
		detail.Versions = append(detail.Versions, view)
	}
	return detail, nil
}

func (s *Service) ClonePlanVersion(ctx context.Context, userID, planID string) (*VersionView, error) {
	var created model.PlanVersion
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", planID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		if plan.ActiveVersionID == nil {
			return ErrInvalidState
		}
		var existing int64
		if err := tx.Model(&model.PlanVersion{}).Where("plan_id = ? AND user_id = ? AND status = ?", planID, userID, model.PlanVersionDraft).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrConflict
		}
		var base model.PlanVersion
		if err := tx.Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&base).Error; err != nil {
			return mapNotFound(err)
		}
		created = model.PlanVersion{ID: uuid.NewString(), UserID: userID, PlanID: planID, VersionNo: base.VersionNo + 1, Status: model.PlanVersionDraft, WeeklyCapacityMinutes: base.WeeklyCapacityMinutes, StartDate: base.StartDate, EndDate: base.EndDate, BaseVersionID: &base.ID, BaseStructureRevision: base.StructureRevision, StructureRevision: 1}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		var milestones []model.Milestone
		if err := tx.Where("plan_version_id = ? AND user_id = ?", base.ID, userID).Order("position").Find(&milestones).Error; err != nil {
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
		if err := tx.Where("plan_version_id = ? AND user_id = ? AND status NOT IN ?", base.ID, userID, []string{model.TaskStatusDone, model.TaskStatusCanceled}).Order("position").Find(&tasks).Error; err != nil {
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
			copy := model.Task{ID: uuid.NewString(), UserID: userID, PlanVersionID: created.ID, MilestoneID: milestoneID, CopiedFromTaskID: &sourceID, Title: item.Title, Description: item.Description, EstimateMinutes: item.EstimateMinutes, ScheduledDate: item.ScheduledDate, Position: item.Position, Status: model.TaskStatusTodo, Version: 1}
			if err := tx.Create(&copy).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	view, err := s.versionView(ctx, userID, created)
	return &view, err
}

func (s *Service) ActivatePlanVersion(ctx context.Context, userID, versionID string) (*PlanDetail, error) {
	var planID string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version model.PlanVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", versionID, userID).First(&version).Error; err != nil {
			return mapNotFound(err)
		}
		if version.Status != model.PlanVersionDraft {
			return ErrInvalidState
		}
		var plan model.Plan
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", version.PlanID, userID).First(&plan).Error; err != nil {
			return mapNotFound(err)
		}
		planID = plan.ID
		if plan.ActiveVersionID != nil {
			var active model.PlanVersion
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", *plan.ActiveVersionID, userID).First(&active).Error; err != nil {
				return mapNotFound(err)
			}
			if version.BaseVersionID == nil || *version.BaseVersionID != active.ID || version.BaseStructureRevision != active.StructureRevision {
				return ErrPlanVersionConflict
			}
			var running int64
			if err := tx.Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Where("s.user_id = ? AND s.status = ? AND t.plan_version_id = ?", userID, model.SessionStatusRunning, active.ID).Count(&running).Error; err != nil {
				return err
			}
			if running > 0 {
				return ErrConflict
			}
			if err := tx.Exec(`DELETE draft FROM tasks draft JOIN tasks source ON source.id = draft.copied_from_task_id WHERE draft.user_id = ? AND draft.plan_version_id = ? AND source.status IN (?, ?)`, userID, version.ID, model.TaskStatusDone, model.TaskStatusCanceled).Error; err != nil {
				return err
			}
			now := s.now().UTC()
			if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", active.ID, userID).Updates(map[string]any{"status": model.PlanVersionSuperseded, "superseded_at": now}).Error; err != nil {
				return err
			}
		}
		now := s.now().UTC()
		if err := tx.Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", version.ID, userID).Updates(map[string]any{"status": model.PlanVersionActive, "activated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&model.Plan{}).Where("id = ? AND user_id = ?", plan.ID, userID).Update("active_version_id", version.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, userID, planID)
}

func (s *Service) versionView(ctx context.Context, userID string, version model.PlanVersion) (VersionView, error) {
	view := VersionView{ID: version.ID, VersionNo: version.VersionNo, Status: version.Status, WeeklyCapacityMinutes: version.WeeklyCapacityMinutes, StartDate: formatDatePtr(version.StartDate), EndDate: formatDatePtr(version.EndDate), StructureRevision: version.StructureRevision, ActivatedAt: version.ActivatedAt, SupersededAt: version.SupersededAt, Milestones: []MilestoneView{}, UnassignedTasks: []TaskView{}}
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

func goalNode(goal model.Goal) GoalNode {
	return GoalNode{ID: goal.ID, ParentGoalID: goal.ParentGoalID, Title: goal.Title, Description: goal.Description, SuccessCriteria: goal.SuccessCriteria, TargetDate: formatDatePtr(goal.TargetDate), Status: goal.Status, Version: goal.Version, Plans: []PlanSummary{}, Children: []*GoalNode{}}
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
	item := TaskView{ID: task.ID, PlanID: planID, PlanTitle: planTitle, PlanMode: planMode, PlanVersionID: task.PlanVersionID, MilestoneID: task.MilestoneID, Title: task.Title, Description: task.Description, EstimateMinutes: task.EstimateMinutes, ScheduledDate: formatDatePtr(task.ScheduledDate), Position: task.Position, Status: task.Status, Version: task.Version, CompletedAt: task.CompletedAt}
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
