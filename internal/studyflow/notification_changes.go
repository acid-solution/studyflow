package studyflow

import (
	"context"
	"sort"
	"studyflow/internal/model"
	"time"
)

type NotificationResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Notification struct {
	Sequence   uint64                 `json:"-"`
	Kind       string                 `json:"kind"`
	Resources  []NotificationResource `json:"resources"`
	Topics     []string               `json:"topics"`
	OccurredAt time.Time              `json:"occurred_at"`
}

// NotificationWaker schedules local delivery; it performs no database or network
// work on the request goroutine. The durable event remains in the Outbox.
type NotificationWaker interface {
	Wake(userID string)
}

func (s *Service) withChange(ctx context.Context, userID, kind string, fn func(Repository) error) error {
	recorded := false
	err := s.repo.WithinTransaction(ctx, func(repo Repository) error {
		recorder := &changeRepository{Repository: repo}
		if err := fn(recorder); err != nil {
			return err
		}
		if len(recorder.resources) == 0 {
			return nil
		}
		if err := repo.AppendNotification(ctx, userID, kind, recorder.resources, notificationTopics(recorder.resources), s.now()); err != nil {
			return err
		}
		recorded = true
		return nil
	})
	if err == nil && recorded && s.notifications != nil {
		// Commit has finished. Do not use the request context: the client can
		// disconnect now without canceling delivery of an already committed event.
		s.notifications.Wake(userID)
	}
	return err
}

// Observe successful mutations to suppress notifications for idempotent no-ops.
// The service supplies the operation's meaning and the outer transaction.
type changeRepository struct {
	Repository
	resources []NotificationResource
}

func (r *changeRepository) record(kind, id string) {
	for _, v := range r.resources {
		if v.Type == kind && v.ID == id {
			return
		}
	}
	r.resources = append(r.resources, NotificationResource{kind, id})
}
func notificationTopics(resources []NotificationResource) []string {
	topics := map[string]bool{}
	for _, resource := range resources {
		var names []string
		switch resource.Type {
		case "goal":
			names = []string{"goals", "plans", "plan-detail"}
		case "preferences":
			names = []string{"preferences", "dashboard", "tasks", "reviews"}
		case "import":
			names = []string{"imports", "goals", "plans", "plan-detail", "tasks", "dashboard", "reviews"}
		case "session":
			names = []string{"sessions", "dashboard", "reviews", "tasks", "plan-detail"}
		default:
			names = []string{"goals", "plans", "plan-detail", "tasks", "dashboard", "reviews", "sessions", "imports"}
		}
		for _, name := range names {
			topics[name] = true
		}
	}
	result := make([]string, 0, len(topics))
	for name := range topics {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
func (r *changeRepository) CreateGoal(ctx context.Context, value *model.Goal) error {
	err := r.Repository.CreateGoal(ctx, value)
	if err == nil {
		r.record("goal", value.ID)
	}
	return err
}
func (r *changeRepository) CreatePlan(ctx context.Context, value *model.Plan) error {
	err := r.Repository.CreatePlan(ctx, value)
	if err == nil {
		r.record("plan", value.ID)
	}
	return err
}
func (r *changeRepository) CreatePlanVersion(ctx context.Context, value *model.PlanVersion) error {
	err := r.Repository.CreatePlanVersion(ctx, value)
	if err == nil {
		r.record("plan_version", value.ID)
	}
	return err
}
func (r *changeRepository) CreateMilestone(ctx context.Context, value *model.Milestone) error {
	err := r.Repository.CreateMilestone(ctx, value)
	if err == nil {
		r.record("milestone", value.ID)
	}
	return err
}
func (r *changeRepository) CreateTask(ctx context.Context, value *model.Task) error {
	err := r.Repository.CreateTask(ctx, value)
	if err == nil {
		r.record("task", value.ID)
	}
	return err
}
func (r *changeRepository) CreateSession(ctx context.Context, value *model.StudySession) error {
	err := r.Repository.CreateSession(ctx, value)
	if err == nil {
		r.record("session", value.ID)
	}
	return err
}
func (r *changeRepository) CreatePlanImport(ctx context.Context, value *model.PlanImport) error {
	err := r.Repository.CreatePlanImport(ctx, value)
	if err == nil {
		r.record("import", value.ID)
	}
	return err
}
func (r *changeRepository) CreateLearningRouteImport(ctx context.Context, value *model.LearningRouteImport) error {
	err := r.Repository.CreateLearningRouteImport(ctx, value)
	if err == nil {
		r.record("import", value.ID)
	}
	return err
}
func (r *changeRepository) UpdateGoal(ctx context.Context, userID, id string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	ok, err := r.Repository.UpdateGoal(ctx, userID, id, expected, changes, increment)
	if err == nil && ok {
		r.record("goal", id)
	}
	return ok, err
}
func (r *changeRepository) UpdatePlan(ctx context.Context, userID, id string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	ok, err := r.Repository.UpdatePlan(ctx, userID, id, expected, changes, increment)
	if err == nil && ok {
		r.record("plan", id)
	}
	return ok, err
}
func (r *changeRepository) UpdateTask(ctx context.Context, userID, id string, expected *uint, changes map[string]any, increment bool) (bool, error) {
	ok, err := r.Repository.UpdateTask(ctx, userID, id, expected, changes, increment)
	if err == nil && ok {
		r.record("task", id)
	}
	return ok, err
}
func (r *changeRepository) UpdatePlanVersion(ctx context.Context, userID, id string, changes map[string]any) error {
	err := r.Repository.UpdatePlanVersion(ctx, userID, id, changes)
	if err == nil {
		r.record("plan_version", id)
	}
	return err
}
func (r *changeRepository) UpdateMilestone(ctx context.Context, userID, id string, changes map[string]any) error {
	err := r.Repository.UpdateMilestone(ctx, userID, id, changes)
	if err == nil {
		r.record("milestone", id)
	}
	return err
}
func (r *changeRepository) UpdateSession(ctx context.Context, userID, id string, changes map[string]any) error {
	err := r.Repository.UpdateSession(ctx, userID, id, changes)
	if err == nil {
		r.record("session", id)
	}
	return err
}
func (r *changeRepository) DeleteTask(ctx context.Context, userID, id string) error {
	err := r.Repository.DeleteTask(ctx, userID, id)
	if err == nil {
		r.record("task", id)
	}
	return err
}
func (r *changeRepository) DeleteMilestoneWithTasks(ctx context.Context, userID, id string) error {
	err := r.Repository.DeleteMilestoneWithTasks(ctx, userID, id)
	if err == nil {
		r.record("milestone", id)
	}
	return err
}
func (r *changeRepository) BumpStructure(ctx context.Context, userID, id string) error {
	err := r.Repository.BumpStructure(ctx, userID, id)
	if err == nil {
		r.record("plan_version", id)
	}
	return err
}
func (r *changeRepository) UpdatePreference(ctx context.Context, userID string, changes map[string]any) error {
	err := r.Repository.UpdatePreference(ctx, userID, changes)
	if err == nil {
		r.record("preferences", userID)
	}
	return err
}
func (r *changeRepository) ResetTaskIfInProgress(ctx context.Context, userID, id string) error {
	task, err := r.Repository.Task(ctx, userID, id, true)
	if err != nil {
		return err
	}
	err = r.Repository.ResetTaskIfInProgress(ctx, userID, id)
	if err == nil && task.Status == model.TaskStatusInProgress {
		r.record("task", id)
	}
	return err
}
