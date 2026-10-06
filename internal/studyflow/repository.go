package studyflow

import (
	"context"
	"errors"
	"strings"
	"time"

	"studyflow/internal/model"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the persistence boundary used by Service. It owns all GORM
// queries and exposes operations in StudyFlow's domain vocabulary. A repository
// passed to WithinTransaction uses the same database transaction, which lets the
// service keep business decisions and transaction boundaries together without
// depending on GORM.
type Repository interface {
	WithinTransaction(context.Context, func(Repository) error) error

	CreateGoal(context.Context, *model.Goal) error
	Goal(context.Context, string, string, bool) (*model.Goal, error)
	Goals(context.Context, string) ([]model.Goal, error)
	ChildGoals(context.Context, string, string) ([]model.Goal, error)
	UpdateGoal(context.Context, string, string, *uint, map[string]any, bool) (bool, error)

	CreatePlan(context.Context, *model.Plan) error
	Plan(context.Context, string, string, bool) (*model.Plan, error)
	Plans(context.Context, string, PlanRepositoryFilter) ([]model.Plan, error)
	PlansByGoal(context.Context, string, string) ([]model.Plan, error)
	UpdatePlan(context.Context, string, string, *uint, map[string]any, bool) (bool, error)

	CreatePlanVersion(context.Context, *model.PlanVersion) error
	PlanVersion(context.Context, string, string, bool) (*model.PlanVersion, error)
	PlanVersionInPlan(context.Context, string, string, string) (*model.PlanVersion, error)
	PlanVersions(context.Context, string, string) ([]model.PlanVersion, error)
	PlanVersionsByStatus(context.Context, string, []string) ([]model.PlanVersion, error)
	CountPlanVersions(context.Context, string, string, string) (int64, error)
	MaxPlanVersionNumber(context.Context, string, string) (uint, error)
	UpdatePlanVersion(context.Context, string, string, map[string]any) error

	CreateMilestone(context.Context, *model.Milestone) error
	Milestone(context.Context, string, string, bool) (*model.Milestone, error)
	MilestonesByVersion(context.Context, string, string) ([]model.Milestone, error)
	MilestoneExists(context.Context, string, string, string) (bool, error)
	MaxMilestonePosition(context.Context, string, string) (uint, error)
	UpdateMilestone(context.Context, string, string, map[string]any) error
	DeleteMilestoneWithTasks(context.Context, string, string) error

	CreateTask(context.Context, *model.Task) error
	Task(context.Context, string, string, bool) (*model.Task, error)
	Tasks(context.Context, string) ([]model.Task, error)
	TasksByVersion(context.Context, string, string) ([]model.Task, error)
	TasksByMilestone(context.Context, string, string) ([]model.Task, error)
	TaskExists(context.Context, string, string) (bool, error)
	MaxTaskPosition(context.Context, string, string, *string) (uint, error)
	CountTasks(context.Context, string, string, []string, []string) (int64, error)
	CountScheduledTasks(context.Context, string, string) (int64, error)
	UpdateTask(context.Context, string, string, *uint, map[string]any, bool) (bool, error)
	ResetTaskIfInProgress(context.Context, string, string) error
	DeleteTask(context.Context, string, string) error
	ListTaskRecords(context.Context, string, TaskFilter) ([]TaskRecord, error)

	CreateScheduleEvent(context.Context, *model.TaskScheduleEvent) error
	BumpStructure(context.Context, string, string) error

	CreateSession(context.Context, *model.StudySession) error
	Session(context.Context, string, string, bool) (*model.StudySession, error)
	RunningSessionForTask(context.Context, string, string, bool) (*model.StudySession, error)
	SessionsByTask(context.Context, string, string) ([]model.StudySession, error)
	CountSessions(context.Context, string, string, string) (int64, error)
	CountRunningSessionsForVersion(context.Context, string, string) (int64, error)
	CountRunningSessionsForPlan(context.Context, string, string) (int64, error)
	UpdateSession(context.Context, string, string, map[string]any) error
	RunningSessionRecord(context.Context, string) (*RunningSessionRecord, error)

	Preference(context.Context, string) (*model.UserPreference, error)
	CreatePreferenceIfMissing(context.Context, *model.UserPreference) error
	UpdatePreference(context.Context, string, map[string]any) error

	CreatePlanImport(context.Context, *model.PlanImport) error
	PlanImport(context.Context, string, string) (*model.PlanImport, error)
	PlanImports(context.Context, string, int) ([]model.PlanImport, error)
	CountsByVersion(context.Context, string, []string, bool) ([]VersionCountRow, error)
	PlansByIDs(context.Context, string, []string) ([]model.Plan, error)

	ActivePlans(context.Context, string) ([]model.Plan, error)
	ReviewSessions(context.Context, string, time.Time, time.Time) ([]ReviewSessionRow, error)
	CompletedTasks(context.Context, string, time.Time, time.Time) ([]CompletedTaskRow, error)
	ScheduledTasks(context.Context, string, time.Time, time.Time) ([]ScheduledTaskRow, error)
	CurrentOverdueCount(context.Context, string, time.Time) (int64, error)
	Reschedules(context.Context, string, time.Time, time.Time) ([]RescheduleRow, error)
}

type PlanRepositoryFilter struct {
	GoalID      string
	Status      string
	OldestFirst bool
}

type TaskRecord struct {
	model.Task
	PlanID    string
	PlanTitle string
	PlanMode  string
}

type RunningSessionRecord struct {
	model.StudySession
	TaskTitle     string
	PlanVersionID string
	TaskVersion   uint
	TaskStatus    string
	PlanID        string
	PlanTitle     string
	PlanMode      string
}

type VersionCountRow struct {
	PlanVersionID string
	Total         int
}

type ReviewSessionRow struct {
	DurationSeconds uint64
	StartedAt       time.Time
	PlanID          string
}

type CompletedTaskRow struct {
	CompletedAt   time.Time
	ScheduledDate *time.Time
	PlanID        string
	PlanMode      string
}

type ScheduledTaskRow struct {
	ScheduledDate time.Time
	CompletedAt   *time.Time
}

type RescheduleRow struct {
	PlanID string
	Count  int
}

type gormRepository struct{ db *gorm.DB }

var _ Repository = (*gormRepository)(nil)

func NewGORMRepository(db *gorm.DB) Repository { return &gormRepository{db: db} }

// NewService preserves the application's existing construction API while
// making the GORM dependency an infrastructure detail of the repository.
func NewService(db *gorm.DB) *Service {
	return NewServiceWithRepository(NewGORMRepository(db), nil)
}

// NewServiceWithCache builds a service with a GORM-backed repository and the
// supplied read-model cache.
func NewServiceWithCache(db *gorm.DB, cache Cache) *Service {
	return NewServiceWithRepository(NewGORMRepository(db), cache)
}

// NewServiceWithRepository is useful for focused unit tests and alternative
// persistence implementations. Passing a nil cache disables caching.
func NewServiceWithRepository(repo Repository, cache Cache) *Service {
	if cache == nil {
		cache = noopCache{}
	}
	return &Service{repo: repo, now: time.Now, cache: cache}
}

func (r *gormRepository) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormRepository{db: tx})
	})
}

func (r *gormRepository) CreateGoal(ctx context.Context, value *model.Goal) error {
	return r.db.WithContext(ctx).Create(value).Error
}

func (r *gormRepository) Goal(ctx context.Context, userID, goalID string, lock bool) (*model.Goal, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.Goal
	if err := query.Where("id = ? AND user_id = ?", goalID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) Goals(ctx context.Context, userID string) ([]model.Goal, error) {
	var values []model.Goal
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at, id").Find(&values).Error
	return values, err
}

func (r *gormRepository) ChildGoals(ctx context.Context, userID, goalID string) ([]model.Goal, error) {
	var values []model.Goal
	err := r.db.WithContext(ctx).Where("user_id = ? AND parent_goal_id = ?", userID, goalID).Find(&values).Error
	return values, err
}

func (r *gormRepository) UpdateGoal(ctx context.Context, userID, goalID string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	if increment {
		changes = cloneChanges(changes)
		changes["version"] = gorm.Expr("version + 1")
	}
	query := r.db.WithContext(ctx).Model(&model.Goal{}).Where("id = ? AND user_id = ?", goalID, userID)
	if expected != nil {
		query = query.Where("version = ?", *expected)
	}
	result := query.Updates(changes)
	return result.RowsAffected == 1, result.Error
}

func (r *gormRepository) CreatePlan(ctx context.Context, value *model.Plan) error {
	return r.db.WithContext(ctx).Create(value).Error
}

func (r *gormRepository) Plan(ctx context.Context, userID, planID string, lock bool) (*model.Plan, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.Plan
	if err := query.Where("id = ? AND user_id = ?", planID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) Plans(ctx context.Context, userID string, filter PlanRepositoryFilter) ([]model.Plan, error) {
	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if filter.GoalID != "" {
		query = query.Where("goal_id = ?", filter.GoalID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	order := "created_at DESC"
	if filter.OldestFirst {
		order = "created_at, id"
	}
	var values []model.Plan
	err := query.Order(order).Find(&values).Error
	return values, err
}

func (r *gormRepository) PlansByGoal(ctx context.Context, userID, goalID string) ([]model.Plan, error) {
	var values []model.Plan
	err := r.db.WithContext(ctx).Where("user_id = ? AND goal_id = ?", userID, goalID).Find(&values).Error
	return values, err
}

func (r *gormRepository) UpdatePlan(ctx context.Context, userID, planID string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	if increment {
		changes = cloneChanges(changes)
		changes["revision"] = gorm.Expr("revision + 1")
	}
	query := r.db.WithContext(ctx).Model(&model.Plan{}).Where("id = ? AND user_id = ?", planID, userID)
	if expected != nil {
		query = query.Where("revision = ?", *expected)
	}
	result := query.Updates(changes)
	return result.RowsAffected == 1, result.Error
}

func (r *gormRepository) CreatePlanVersion(ctx context.Context, value *model.PlanVersion) error {
	err := r.db.WithContext(ctx).Create(value).Error
	if repositoryDuplicate(err) {
		return ErrConflict
	}
	return err
}

func (r *gormRepository) PlanVersion(ctx context.Context, userID, versionID string, lock bool) (*model.PlanVersion, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.PlanVersion
	if err := query.Where("id = ? AND user_id = ?", versionID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) PlanVersionInPlan(ctx context.Context, userID, planID, versionID string) (*model.PlanVersion, error) {
	var value model.PlanVersion
	if err := r.db.WithContext(ctx).Where("id = ? AND plan_id = ? AND user_id = ?", versionID, planID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) PlanVersions(ctx context.Context, userID, planID string) ([]model.PlanVersion, error) {
	var values []model.PlanVersion
	err := r.db.WithContext(ctx).Where("plan_id = ? AND user_id = ?", planID, userID).Order("version_no DESC").Find(&values).Error
	return values, err
}

func (r *gormRepository) PlanVersionsByStatus(ctx context.Context, userID string, statuses []string) ([]model.PlanVersion, error) {
	var values []model.PlanVersion
	err := r.db.WithContext(ctx).Where("user_id = ? AND status IN ?", userID, statuses).Find(&values).Error
	return values, err
}

func (r *gormRepository) CountPlanVersions(ctx context.Context, userID, planID, status string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.PlanVersion{}).Where("plan_id = ? AND user_id = ? AND status = ?", planID, userID, status).Count(&count).Error
	return count, err
}

func (r *gormRepository) MaxPlanVersionNumber(ctx context.Context, userID, planID string) (uint, error) {
	var value uint
	err := r.db.WithContext(ctx).Model(&model.PlanVersion{}).Where("plan_id = ? AND user_id = ?", planID, userID).Select("COALESCE(MAX(version_no), 0)").Scan(&value).Error
	return value, err
}

func (r *gormRepository) UpdatePlanVersion(ctx context.Context, userID, versionID string, changes map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", versionID, userID).Updates(changes).Error
}

func (r *gormRepository) CreateMilestone(ctx context.Context, value *model.Milestone) error {
	err := r.db.WithContext(ctx).Create(value).Error
	if repositoryDuplicate(err) {
		return ErrConflict
	}
	return err
}

func (r *gormRepository) Milestone(ctx context.Context, userID, milestoneID string, lock bool) (*model.Milestone, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.Milestone
	if err := query.Where("id = ? AND user_id = ?", milestoneID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) MilestonesByVersion(ctx context.Context, userID, versionID string) ([]model.Milestone, error) {
	var values []model.Milestone
	err := r.db.WithContext(ctx).Where("plan_version_id = ? AND user_id = ?", versionID, userID).Order("position, id").Find(&values).Error
	return values, err
}

func (r *gormRepository) MilestoneExists(ctx context.Context, userID, versionID, milestoneID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Milestone{}).Where("id = ? AND user_id = ? AND plan_version_id = ?", milestoneID, userID, versionID).Count(&count).Error
	return count == 1, err
}

func (r *gormRepository) MaxMilestonePosition(ctx context.Context, userID, versionID string) (uint, error) {
	var value uint
	err := r.db.WithContext(ctx).Model(&model.Milestone{}).Where("plan_version_id = ? AND user_id = ?", versionID, userID).Select("COALESCE(MAX(position), 0)").Scan(&value).Error
	return value, err
}

func (r *gormRepository) UpdateMilestone(ctx context.Context, userID, milestoneID string, changes map[string]any) error {
	err := r.db.WithContext(ctx).Model(&model.Milestone{}).Where("id = ? AND user_id = ?", milestoneID, userID).Updates(changes).Error
	if repositoryDuplicate(err) {
		return ErrConflict
	}
	return err
}

func (r *gormRepository) DeleteMilestoneWithTasks(ctx context.Context, userID, milestoneID string) error {
	if err := r.db.WithContext(ctx).Where("milestone_id = ? AND user_id = ?", milestoneID, userID).Delete(&model.Task{}).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("id = ? AND user_id = ?", milestoneID, userID).Delete(&model.Milestone{}).Error
}

func (r *gormRepository) CreateTask(ctx context.Context, value *model.Task) error {
	return r.db.WithContext(ctx).Create(value).Error
}

func (r *gormRepository) Task(ctx context.Context, userID, taskID string, lock bool) (*model.Task, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.Task
	if err := query.Where("id = ? AND user_id = ?", taskID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) Tasks(ctx context.Context, userID string) ([]model.Task, error) {
	var values []model.Task
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&values).Error
	return values, err
}

func (r *gormRepository) TasksByVersion(ctx context.Context, userID, versionID string) ([]model.Task, error) {
	var values []model.Task
	err := r.db.WithContext(ctx).Where("plan_version_id = ? AND user_id = ?", versionID, userID).Order("position, id").Find(&values).Error
	return values, err
}

func (r *gormRepository) TasksByMilestone(ctx context.Context, userID, milestoneID string) ([]model.Task, error) {
	var values []model.Task
	err := r.db.WithContext(ctx).Where("milestone_id = ? AND user_id = ?", milestoneID, userID).Order("position").Find(&values).Error
	return values, err
}

func (r *gormRepository) TaskExists(ctx context.Context, userID, taskID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND user_id = ?", taskID, userID).Count(&count).Error
	return count == 1, err
}

func (r *gormRepository) MaxTaskPosition(ctx context.Context, userID, versionID string, milestoneID *string) (uint, error) {
	query := r.db.WithContext(ctx).Model(&model.Task{}).Where("plan_version_id = ? AND user_id = ?", versionID, userID)
	if milestoneID == nil {
		query = query.Where("milestone_id IS NULL")
	} else {
		query = query.Where("milestone_id = ?", *milestoneID)
	}
	var value uint
	err := query.Select("COALESCE(MAX(position), 0)").Scan(&value).Error
	return value, err
}

func (r *gormRepository) CountTasks(ctx context.Context, userID, versionID string, includeStatuses, excludeStatuses []string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Task{}).Where("user_id = ? AND plan_version_id = ?", userID, versionID)
	if len(includeStatuses) > 0 {
		query = query.Where("status IN ?", includeStatuses)
	}
	if len(excludeStatuses) > 0 {
		query = query.Where("status NOT IN ?", excludeStatuses)
	}
	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *gormRepository) CountScheduledTasks(ctx context.Context, userID, versionID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.Task{}).Where("user_id = ? AND plan_version_id = ? AND scheduled_date IS NOT NULL", userID, versionID).Count(&count).Error
	return count, err
}

func (r *gormRepository) UpdateTask(ctx context.Context, userID, taskID string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	if increment {
		changes = cloneChanges(changes)
		changes["version"] = gorm.Expr("version + 1")
	}
	query := r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND user_id = ?", taskID, userID)
	if expected != nil {
		query = query.Where("version = ?", *expected)
	}
	result := query.Updates(changes)
	return result.RowsAffected == 1, result.Error
}

func (r *gormRepository) ResetTaskIfInProgress(ctx context.Context, userID, taskID string) error {
	return r.db.WithContext(ctx).Model(&model.Task{}).
		Where("id = ? AND user_id = ? AND status = ?", taskID, userID, model.TaskStatusInProgress).
		Updates(map[string]any{"status": model.TaskStatusTodo, "version": gorm.Expr("version + 1")}).Error
}

func (r *gormRepository) DeleteTask(ctx context.Context, userID, taskID string) error {
	return r.db.WithContext(ctx).Where("id = ? AND user_id = ?", taskID, userID).Delete(&model.Task{}).Error
}

func (r *gormRepository) ListTaskRecords(ctx context.Context, userID string, filter TaskFilter) ([]TaskRecord, error) {
	query := r.db.WithContext(ctx).Table("tasks t").Select("t.*, p.id AS plan_id, p.title AS plan_title, pv.mode AS plan_mode").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id AND p.status = ?", model.GoalStatusActive).Where("t.user_id = ?", userID)
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
	var values []TaskRecord
	err := query.Order("t.scheduled_date IS NULL, t.scheduled_date, p.title, t.position, t.id").Scan(&values).Error
	return values, err
}

func (r *gormRepository) CreateScheduleEvent(ctx context.Context, value *model.TaskScheduleEvent) error {
	return r.db.WithContext(ctx).Create(value).Error
}

func (r *gormRepository) BumpStructure(ctx context.Context, userID, versionID string) error {
	result := r.db.WithContext(ctx).Model(&model.PlanVersion{}).Where("id = ? AND user_id = ?", versionID, userID).Update("structure_revision", gorm.Expr("structure_revision + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *gormRepository) CreateSession(ctx context.Context, value *model.StudySession) error {
	err := r.db.WithContext(ctx).Create(value).Error
	if repositoryDuplicate(err) {
		return ErrConflict
	}
	return err
}

func (r *gormRepository) Session(ctx context.Context, userID, sessionID string, lock bool) (*model.StudySession, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.StudySession
	if err := query.Where("id = ? AND user_id = ?", sessionID, userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) RunningSessionForTask(ctx context.Context, userID, taskID string, lock bool) (*model.StudySession, error) {
	query := r.lock(r.db.WithContext(ctx), lock)
	var value model.StudySession
	if err := query.Where("task_id = ? AND user_id = ? AND status = ?", taskID, userID, model.SessionStatusRunning).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) SessionsByTask(ctx context.Context, userID, taskID string) ([]model.StudySession, error) {
	var values []model.StudySession
	err := r.db.WithContext(ctx).Where("task_id = ? AND user_id = ?", taskID, userID).Order("started_at DESC, id DESC").Find(&values).Error
	return values, err
}

func (r *gormRepository) CountSessions(ctx context.Context, userID, taskID, status string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.StudySession{}).Where("task_id = ? AND user_id = ? AND status = ?", taskID, userID, status).Count(&count).Error
	return count, err
}

func (r *gormRepository) CountRunningSessionsForVersion(ctx context.Context, userID, versionID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Where("s.user_id = ? AND s.status = ? AND t.plan_version_id = ?", userID, model.SessionStatusRunning, versionID).Count(&count).Error
	return count, err
}

func (r *gormRepository) CountRunningSessionsForPlan(ctx context.Context, userID, planID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("study_sessions s").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Where("s.user_id = ? AND s.status = ? AND pv.plan_id = ?", userID, model.SessionStatusRunning, planID).Count(&count).Error
	return count, err
}

func (r *gormRepository) UpdateSession(ctx context.Context, userID, sessionID string, changes map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.StudySession{}).Where("id = ? AND user_id = ?", sessionID, userID).Updates(changes).Error
}

func (r *gormRepository) RunningSessionRecord(ctx context.Context, userID string) (*RunningSessionRecord, error) {
	var value RunningSessionRecord
	err := r.db.WithContext(ctx).Table("study_sessions s").Select("s.*, t.title AS task_title, t.plan_version_id, t.version AS task_version, t.status AS task_status, p.id AS plan_id, p.title AS plan_title, pv.mode AS plan_mode").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("s.user_id = ? AND s.status = ?", userID, model.SessionStatusRunning).Take(&value).Error
	if err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) Preference(ctx context.Context, userID string) (*model.UserPreference, error) {
	var value model.UserPreference
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) CreatePreferenceIfMissing(ctx context.Context, value *model.UserPreference) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(value).Error
}

func (r *gormRepository) UpdatePreference(ctx context.Context, userID string, changes map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.UserPreference{}).Where("user_id = ?", userID).Updates(changes).Error
}

func (r *gormRepository) CreatePlanImport(ctx context.Context, value *model.PlanImport) error {
	err := r.db.WithContext(ctx).Create(value).Error
	if repositoryDuplicate(err) {
		return errImportReplay
	}
	return err
}

func (r *gormRepository) PlanImport(ctx context.Context, userID, key string) (*model.PlanImport, error) {
	var value model.PlanImport
	if err := r.db.WithContext(ctx).Where("user_id = ? AND idempotency_key = ?", userID, key).First(&value).Error; err != nil {
		return nil, repositoryNotFound(err)
	}
	return &value, nil
}

func (r *gormRepository) PlanImports(ctx context.Context, userID string, limit int) ([]model.PlanImport, error) {
	var values []model.PlanImport
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(limit).Find(&values).Error
	return values, err
}

func (r *gormRepository) CountsByVersion(ctx context.Context, userID string, versionIDs []string, milestones bool) ([]VersionCountRow, error) {
	query := r.db.WithContext(ctx)
	if milestones {
		query = query.Model(&model.Milestone{})
	} else {
		query = query.Model(&model.Task{})
	}
	var values []VersionCountRow
	err := query.Select("plan_version_id, COUNT(*) AS total").Where("user_id = ? AND plan_version_id IN ?", userID, versionIDs).Group("plan_version_id").Scan(&values).Error
	return values, err
}

func (r *gormRepository) PlansByIDs(ctx context.Context, userID string, planIDs []string) ([]model.Plan, error) {
	var values []model.Plan
	err := r.db.WithContext(ctx).Where("user_id = ? AND id IN ?", userID, planIDs).Find(&values).Error
	return values, err
}

func (r *gormRepository) ActivePlans(ctx context.Context, userID string) ([]model.Plan, error) {
	var values []model.Plan
	err := r.db.WithContext(ctx).Where("user_id = ? AND status = ? AND active_version_id IS NOT NULL", userID, model.GoalStatusActive).Order("created_at").Find(&values).Error
	return values, err
}

func (r *gormRepository) ReviewSessions(ctx context.Context, userID string, start, end time.Time) ([]ReviewSessionRow, error) {
	var values []ReviewSessionRow
	err := r.db.WithContext(ctx).Table("study_sessions s").Select("s.duration_seconds, s.started_at, p.id AS plan_id").Joins("JOIN tasks t ON t.id = s.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("s.user_id = ? AND s.status = ? AND s.started_at >= ? AND s.started_at < ?", userID, model.SessionStatusFinished, start, end).Scan(&values).Error
	return values, err
}

func (r *gormRepository) CompletedTasks(ctx context.Context, userID string, start, end time.Time) ([]CompletedTaskRow, error) {
	var values []CompletedTaskRow
	err := r.db.WithContext(ctx).Table("tasks t").Select("t.completed_at, t.scheduled_date, p.id AS plan_id, pv.mode AS plan_mode").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("t.user_id = ? AND t.status = ? AND t.completed_at >= ? AND t.completed_at < ?", userID, model.TaskStatusDone, start, end).Scan(&values).Error
	return values, err
}

func (r *gormRepository) ScheduledTasks(ctx context.Context, userID string, start, end time.Time) ([]ScheduledTaskRow, error) {
	var values []ScheduledTaskRow
	err := r.db.WithContext(ctx).Table("tasks t").Select("t.scheduled_date, t.completed_at").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id AND p.status = ?", model.GoalStatusActive).Where("t.user_id = ? AND pv.mode = ? AND t.status <> ? AND t.scheduled_date >= ? AND t.scheduled_date < ?", userID, model.PlanModeCalendar, model.TaskStatusCanceled, start, end).Scan(&values).Error
	return values, err
}

func (r *gormRepository) CurrentOverdueCount(ctx context.Context, userID string, today time.Time) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table("tasks t").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id AND p.active_version_id = pv.id AND p.status = ?", model.GoalStatusActive).Where("t.user_id = ? AND pv.mode = ? AND t.status IN ? AND t.scheduled_date < ?", userID, model.PlanModeCalendar, []string{model.TaskStatusTodo, model.TaskStatusInProgress}, today).Count(&count).Error
	return count, err
}

func (r *gormRepository) Reschedules(ctx context.Context, userID string, start, end time.Time) ([]RescheduleRow, error) {
	var values []RescheduleRow
	err := r.db.WithContext(ctx).Table("task_schedule_events e").Select("p.id AS plan_id, COUNT(*) AS count").Joins("JOIN tasks t ON t.id = e.task_id").Joins("JOIN plan_versions pv ON pv.id = t.plan_version_id").Joins("JOIN plans p ON p.id = pv.plan_id").Where("e.user_id = ? AND e.created_at >= ? AND e.created_at < ?", userID, start, end).Group("p.id").Scan(&values).Error
	return values, err
}

func (r *gormRepository) lock(query *gorm.DB, enabled bool) *gorm.DB {
	if enabled {
		return query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return query
}

func cloneChanges(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func repositoryNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func repositoryDuplicate(err error) bool {
	var value *mysql.MySQLError
	return errors.As(err, &value) && value.Number == 1062
}
