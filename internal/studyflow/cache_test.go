package studyflow

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"studyflow/internal/database"
	"studyflow/internal/model"

	"github.com/google/uuid"
)

// fakeCache is an in-memory Cache that round-trips values through JSON exactly
// like the Redis implementation, so a value that cannot survive marshalling —
// a nil slice turning back into null, say — fails here too.
type fakeCache struct {
	mu     sync.Mutex
	items  map[string][]byte
	gens   map[string]int64
	writes int
}

func newFakeCache() *fakeCache {
	return &fakeCache{items: map[string][]byte{}, gens: map[string]int64{}}
}

func (f *fakeCache) Read(_ context.Context, key string, target any) bool {
	f.mu.Lock()
	raw, ok := f.items[key]
	f.mu.Unlock()
	if !ok {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

func (f *fakeCache) Write(_ context.Context, key string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.items[key] = raw
	f.writes++
	f.mu.Unlock()
}

func (f *fakeCache) Generation(_ context.Context, userID string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gens[userID], true
}

func (f *fakeCache) Bump(_ context.Context, userID string) {
	f.mu.Lock()
	f.gens[userID]++
	f.mu.Unlock()
}

func TestGoalTreeCacheIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeCache()
	service := NewServiceWithCache(db, store)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "缓存目标"})
	if err != nil {
		t.Fatal(err)
	}

	// 1. A read fills the cache. A change made behind the service's back must then
	// be invisible, which is the only way to tell the second read was served from
	// the cache rather than from MySQL.
	first, err := service.GoalTree(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Title != "缓存目标" {
		t.Fatalf("unexpected first read: %+v", first)
	}
	if store.writes == 0 {
		t.Fatal("the first read should have filled the cache")
	}
	if err := db.Exec("UPDATE goals SET title = ? WHERE id = ?", "绕过服务层的修改", goal.ID).Error; err != nil {
		t.Fatal(err)
	}
	second, err := service.GoalTree(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Title != "缓存目标" {
		t.Fatalf("the second read should have come from the cache, got %q", second[0].Title)
	}

	// 2. A write through the service invalidates, so the next read sees the change.
	if _, err := service.UpdateGoal(ctx, userID, goal.ID, UpdateGoalInput{Version: goal.Version, Title: stringPointer("通过服务改名")}); err != nil {
		t.Fatal(err)
	}
	third, err := service.GoalTree(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if third[0].Title != "通过服务改名" {
		t.Fatalf("a write must invalidate the cache, still seeing %q", third[0].Title)
	}

	// 3. Every write path has to bump the generation; a path that forgets is the
	// one failure mode this design has. Each step builds its own fixtures, then
	// records the counter, runs the write and checks it moved.
	generation := func() int64 {
		value, _ := store.Generation(ctx, userID)
		return value
	}
	steps := []struct {
		name string
		run  func() error
	}{
		{"CreateGoal", func() error {
			_, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "新目标"})
			return err
		}},
		{"UpdateGoal", func() error {
			created, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "待改名"})
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.UpdateGoal(ctx, userID, created.ID, UpdateGoalInput{Version: created.Version, Title: stringPointer("改过了")}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("UpdateGoal did not invalidate")
			}
			return nil
		}},
		{"SetGoalStatus", func() error {
			created, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "待放弃"})
			if err != nil {
				return err
			}
			before := generation()
			if err := service.SetGoalStatus(ctx, userID, created.ID, model.GoalStatusAbandoned); err != nil {
				return err
			}
			if generation() == before {
				t.Error("SetGoalStatus did not invalidate")
			}
			return nil
		}},
		{"CreatePlan", func() error {
			before := generation()
			if _, err := service.CreatePlan(ctx, userID, goal.ID, CreatePlanInput{Title: "缓存计划", Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("CreatePlan did not invalidate")
			}
			return nil
		}},
		{"UpdatePlan", func() error {
			plan, err := service.CreatePlan(ctx, userID, goal.ID, CreatePlanInput{Title: "待改计划", Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60})
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.UpdatePlan(ctx, userID, plan.Plan.ID, UpdatePlanInput{Title: stringPointer("改过的计划")}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("UpdatePlan did not invalidate")
			}
			return nil
		}},
		{"ClonePlanVersion", func() error {
			plan, err := service.CreatePlan(ctx, userID, goal.ID, CreatePlanInput{Title: "待复制", Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60})
			if err != nil {
				return err
			}
			if _, err := service.ActivatePlanVersion(ctx, userID, plan.Versions[0].ID); err != nil {
				return err
			}
			before := generation()
			if _, err := service.ClonePlanVersion(ctx, userID, plan.Plan.ID); err != nil {
				return err
			}
			if generation() == before {
				t.Error("ClonePlanVersion did not invalidate")
			}
			return nil
		}},
		{"ActivatePlanVersion", func() error {
			plan, err := service.CreatePlan(ctx, userID, goal.ID, CreatePlanInput{Title: "待激活", Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60})
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.ActivatePlanVersion(ctx, userID, plan.Versions[0].ID); err != nil {
				return err
			}
			if generation() == before {
				t.Error("ActivatePlanVersion did not invalidate")
			}
			return nil
		}},
		{"CreateMilestone", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "带阶段的计划")
			before := generation()
			if _, err := service.CreateMilestone(ctx, userID, version, CreateMilestoneInput{Title: "阶段"}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("CreateMilestone did not invalidate")
			}
			return nil
		}},
		{"UpdateMilestone", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "待改阶段")
			milestone, err := service.CreateMilestone(ctx, userID, version, CreateMilestoneInput{Title: "阶段"})
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.UpdateMilestone(ctx, userID, milestone.ID, UpdateMilestoneInput{Title: stringPointer("改过的阶段")}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("UpdateMilestone did not invalidate")
			}
			return nil
		}},
		{"DeleteMilestone", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "待删阶段")
			milestone, err := service.CreateMilestone(ctx, userID, version, CreateMilestoneInput{Title: "阶段"})
			if err != nil {
				return err
			}
			before := generation()
			if err := service.DeleteMilestone(ctx, userID, milestone.ID); err != nil {
				return err
			}
			if generation() == before {
				t.Error("DeleteMilestone did not invalidate")
			}
			return nil
		}},
		{"CreateTask", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "带任务的计划")
			before := generation()
			if _, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("CreateTask did not invalidate")
			}
			return nil
		}},
		{"UpdateTask", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "待改任务的计划")
			task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"})
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.UpdateTask(ctx, userID, task.ID, UpdateTaskInput{Version: task.Version, Title: stringPointer("改过的任务")}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("UpdateTask did not invalidate")
			}
			return nil
		}},
		{"DeleteTask", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "待删任务的计划")
			task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"})
			if err != nil {
				return err
			}
			before := generation()
			if err := service.DeleteTask(ctx, userID, task.ID); err != nil {
				return err
			}
			if generation() == before {
				t.Error("DeleteTask did not invalidate")
			}
			return nil
		}},
		{"SetTaskStatus", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "待完成任务的计划")
			task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"})
			if err != nil {
				return err
			}
			// Completing and timing only apply to an active version.
			if _, err := service.ActivatePlanVersion(ctx, userID, version); err != nil {
				return err
			}
			before := generation()
			if _, err := service.SetTaskStatus(ctx, userID, task.ID, "complete"); err != nil {
				return err
			}
			if generation() == before {
				t.Error("SetTaskStatus did not invalidate")
			}
			return nil
		}},
		{"StartSession", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "计时的计划")
			task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"})
			if err != nil {
				return err
			}
			if _, err := service.ActivatePlanVersion(ctx, userID, version); err != nil {
				return err
			}
			before := generation()
			session, err := service.StartSession(ctx, userID, task.ID)
			if err != nil {
				return err
			}
			if generation() == before {
				t.Error("StartSession did not invalidate")
			}
			// Only one timer may run per user, so this step has to put its own away
			// before the next one starts another.
			_, err = service.DiscardSession(ctx, userID, session.ID)
			return err
		}},
		{"FinishSession", func() error {
			version := draftVersion(t, service, ctx, userID, goal.ID, "结束计时的计划")
			task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "任务"})
			if err != nil {
				return err
			}
			if _, err := service.ActivatePlanVersion(ctx, userID, version); err != nil {
				return err
			}
			session, err := service.StartSession(ctx, userID, task.ID)
			if err != nil {
				return err
			}
			before := generation()
			if _, err := service.FinishSession(ctx, userID, session.ID, FinishSessionInput{}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("FinishSession did not invalidate")
			}
			return nil
		}},
		{"ImportPlanTree", func() error {
			before := generation()
			if _, err := service.ImportPlanTree(ctx, userID, "cache-invalidate-key", ImportPlanTreeInput{
				GoalID: goal.ID, Title: "导入的计划", Mode: model.PlanModeCalendar,
				Milestones: []ImportMilestoneInput{{Title: "阶段", Tasks: []ImportTaskInput{{Title: "任务"}}}},
			}); err != nil {
				return err
			}
			if generation() == before {
				t.Error("ImportPlanTree did not invalidate")
			}
			return nil
		}},
	}
	for _, step := range steps {
		before := generation()
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if generation() == before {
			t.Fatalf("%s did not invalidate the cache", step.name)
		}
	}

	// 4. A service built without a cache must behave exactly as before.
	plain := NewService(db)
	if _, err := plain.GoalTree(ctx, userID); err != nil {
		t.Fatalf("an uncached service must still read the tree: %v", err)
	}
}

// draftVersion creates a plan under the goal and returns its V1 draft id.
func draftVersion(t *testing.T, service *Service, ctx context.Context, userID, goalID, title string) string {
	t.Helper()
	plan, err := service.CreatePlan(ctx, userID, goalID, CreatePlanInput{Title: title, Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	return plan.Versions[0].ID
}

func TestTaskListCacheIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeCache()
	service := NewServiceWithCache(db, store)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "任务列表缓存目标"})
	if err != nil {
		t.Fatal(err)
	}
	version := draftVersion(t, service, ctx, userID, goal.ID, "任务列表缓存计划")
	task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "缓存任务"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, version); err != nil {
		t.Fatal(err)
	}

	// 1. The unfiltered list is cached, so a change made behind the service's back
	// stays invisible on the next read.
	if _, err := service.ListTasks(ctx, userID, TaskFilter{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tasks SET title = ? WHERE id = ?", "绕过服务层的修改", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	second, err := service.ListTasks(ctx, userID, TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Title != "缓存任务" {
		t.Fatalf("the unfiltered read should have come from the cache: %+v", second)
	}

	// 2. A filtered read goes straight to the database and therefore sees it.
	filtered, err := service.ListTasks(ctx, userID, TaskFilter{Status: model.TaskStatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Title != "绕过服务层的修改" {
		t.Fatalf("a filtered read must not be served from the cache: %+v", filtered)
	}

	// 3. A write through the service invalidates the cached list.
	if _, err := service.UpdateTask(ctx, userID, task.ID, UpdateTaskInput{Version: task.Version, Title: stringPointer("通过服务改名")}); err != nil {
		t.Fatal(err)
	}
	third, err := service.ListTasks(ctx, userID, TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || third[0].Title != "通过服务改名" {
		t.Fatalf("a write must invalidate the cached list: %+v", third)
	}
}

func TestWeeklyReviewCacheIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeCache()
	service := NewServiceWithCache(db, store)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "复盘缓存目标"})
	if err != nil {
		t.Fatal(err)
	}
	version := draftVersion(t, service, ctx, userID, goal.ID, "复盘缓存计划")
	// A date inside the current week, so the task counts towards the review.
	today := dateOnly(service.now().UTC())
	task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "复盘任务", ScheduledDate: stringPointer(today.Format("2006-01-02"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, version); err != nil {
		t.Fatal(err)
	}

	// 1. The first read fills the cache.
	first, err := service.WeeklyReview(ctx, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.CompletedTasks != 0 {
		t.Fatalf("nothing is done yet: %+v", first)
	}

	// 2. A change made behind the service's back stays invisible.
	if err := db.Exec("UPDATE tasks SET status = 'done', completed_at = ? WHERE id = ?", service.now().UTC(), task.ID).Error; err != nil {
		t.Fatal(err)
	}
	second, err := service.WeeklyReview(ctx, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.CompletedTasks != 0 {
		t.Fatalf("the second read should have come from the cache: %+v", second)
	}

	// 3. A write through the service invalidates, and the next read is fresh.
	if err := db.Exec("UPDATE tasks SET status = 'todo', completed_at = NULL WHERE id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetTaskStatus(ctx, userID, task.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	third, err := service.WeeklyReview(ctx, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if third.CompletedTasks != 1 {
		t.Fatalf("a write must invalidate the review cache: %+v", third)
	}
}
