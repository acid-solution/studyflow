package studyflow

import (
	"errors"
	"github.com/google/uuid"
	"os"
	"studyflow/internal/database"
	"studyflow/internal/model"
	"sync"
	"testing"
)

func testRoutePayload() LearningRouteInput {
	plan := func(role string) RoutePlanInput {
		return RoutePlanInput{Role: role, Plan: ImportPlanTreeInput{Title: role, Objective: "可检查成果", Mode: "sequence", WeeklyCapacityMinutes: 120, Milestones: []ImportMilestoneInput{{Title: "第一阶段", Outcome: "提交成果", Tasks: []ImportTaskInput{{Title: "完成可演示任务", Description: "执行说明与验收标准", EstimateMinutes: 30}}}}}}
	}
	return LearningRouteInput{SourceRouteID: uuid.NewString(), SourceVersion: 1, GoalTitle: "后端实习", WeeklyTotalMinutes: 300, Plans: []RoutePlanInput{plan("project"), plan("knowledge")}}
}
func TestLearningRouteAtomicImportAndIndependentVersions(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	user := uuid.NewString()
	defer cleanupUser(t, db, user)
	ctx := t.Context()
	payload := testRoutePayload()
	key := uuid.NewString()
	result, err := s.ImportLearningRoute(ctx, user, key, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Plans) != 2 || countRows(t, db, "goals", user) != 1 || countRows(t, db, "plans", user) != 2 {
		t.Fatal("first import did not create exact package")
	}
	for _, p := range result.Plans {
		v, e := s.repo.PlanVersion(ctx, user, p.PlanVersionID, false)
		if e != nil || v.Status != model.PlanVersionDraft {
			t.Fatal("auto activated", e)
		}
	}
	replay, err := s.ImportLearningRoute(ctx, user, key, payload)
	if err != nil || !replay.Replayed || replay.ImportID != result.ImportID {
		t.Fatal("replay failed", err)
	}
	if _, err = s.GetLearningRouteImport(ctx, uuid.NewString(), key); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account receipt leak")
	}
	copy := payload
	copy.GoalTitle = "changed"
	if _, err = s.ImportLearningRoute(ctx, user, key, copy); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("same key accepted different content")
	}
	for _, p := range result.Plans {
		if _, err = s.ActivatePlanVersion(ctx, user, p.PlanVersionID); err != nil {
			t.Fatal(err)
		}
	}
	project := result.Plans[0]
	knowledge := result.Plans[1]
	tasks, err := s.repo.TasksByVersion(ctx, user, project.PlanVersionID)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve real progress, not merely plan metadata.
	if _, err = s.repo.UpdateTask(ctx, user, tasks[0].ID, nil, map[string]any{"status": model.TaskStatusDone}, false); err != nil {
		t.Fatal(err)
	}
	next := testRoutePayload()
	next.SourceRouteID = payload.SourceRouteID
	next.SourceVersion = 2
	next.Plans = next.Plans[1:]
	next.Plans[0].Plan.Milestones[0].Tasks[0].Title = "更新知识复习任务"
	newer, err := s.ImportLearningRoute(ctx, user, uuid.NewString(), next)
	if err != nil {
		t.Fatal(err)
	}
	if newer.Plans[0].PlanVersionID != project.PlanVersionID || newer.Plans[1].PlanID != knowledge.PlanID || newer.Plans[1].VersionNo != 2 {
		t.Fatal("knowledge-only import modified project")
	}
	existing, err := s.repo.Task(ctx, user, tasks[0].ID, false)
	if err != nil || existing.Status != model.TaskStatusDone {
		t.Fatal("project progress lost")
	}
	next.SourceVersion = 3
	next.Plans[0].Plan.Milestones[0].Tasks[0].Title = "再次更新"
	before := countRows(t, db, "plan_versions", user)
	if _, err = s.ImportLearningRoute(ctx, user, uuid.NewString(), next); !errors.Is(err, ErrConflict) {
		t.Fatal("existing draft overwritten", err)
	}
	if countRows(t, db, "plan_versions", user) != before {
		t.Fatal("conflict left partial import")
	}
	// Invalid second plan after first is built must roll the entire transaction back.
	another := testRoutePayload()
	another.Plans[1].Plan.Milestones[0].Tasks[0].ScheduledDate = strptr("2026-10-20")
	if _, err = s.ImportLearningRoute(ctx, user, uuid.NewString(), another); err == nil {
		t.Fatal("sequence date accepted")
	}
	if countRows(t, db, "goals", user) != 1 || countRows(t, db, "plans", user) != 2 {
		t.Fatal("failed import left goal/plan")
	}
}
func strptr(s string) *string { return &s }

func TestLearningRouteConcurrentReplay(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(db)
	user, key := uuid.NewString(), uuid.NewString()
	defer cleanupUser(t, db, user)
	payload := testRoutePayload()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	ids := make(chan string, 4)
	for range 4 {
		wg.Go(func() {
			r, e := s.ImportLearningRoute(t.Context(), user, key, payload)
			errs <- e
			if r != nil {
				ids <- r.ImportID
			}
		})
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for v := range ids {
		if id != "" && v != id {
			t.Fatal("different receipts")
		}
		id = v
	}
	if countRows(t, db, "plans", user) != 2 || countRows(t, db, "tasks", user) != 2 {
		t.Fatal("duplicate creation")
	}
}
