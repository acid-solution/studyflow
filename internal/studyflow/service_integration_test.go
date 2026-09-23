package studyflow

import (
	"errors"
	"os"
	"testing"
	"time"

	"studyflow/internal/database"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestPlanningExecutionAndReviewIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	userID, otherUserID := uuid.NewString(), uuid.NewString()
	defer cleanupUser(t, db, userID)

	root, err := service.CreateGoal(t.Context(), userID, CreateGoalInput{Title: "拿到 Go 后端实习", SuccessCriteria: "获得 offer"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateGoal(t.Context(), userID, CreateGoalInput{ParentGoalID: &root.ID, Title: "完成项目"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateGoal(t.Context(), userID, root.ID, UpdateGoalInput{Version: root.Version, ParentGoalID: &child.ID})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected cycle validation, got %v", err)
	}

	calendar, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "StudyFlow 开发", Mode: "calendar", WeeklyCapacityMinutes: 600})
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "Go 八股", Mode: "sequence", WeeklyCapacityMinutes: 240})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := service.ListPlans(t.Context(), userID, child.ID, "")
	if err != nil || len(plans) != 2 {
		t.Fatalf("parallel plans = %d, err=%v", len(plans), err)
	}
	sequenceDraft := sequence.Versions[0]
	sequenceTask, err := service.CreateTask(t.Context(), userID, sequenceDraft.ID, CreateTaskInput{Title: "Context 与取消传播", EstimateMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	sequenceNext, err := service.CreateTask(t.Context(), userID, sequenceDraft.ID, CreateTaskInput{Title: "并发控制", EstimateMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, sequenceDraft.ID); err != nil {
		t.Fatal(err)
	}

	draft := calendar.Versions[0]
	milestone, err := service.CreateMilestone(t.Context(), userID, draft.ID, CreateMilestoneInput{Title: "计划闭环"})
	if err != nil {
		t.Fatal(err)
	}
	yesterday, today, tomorrow := "2026-09-22", "2026-09-23", "2026-09-24"
	overdue, err := service.CreateTask(t.Context(), userID, draft.ID, CreateTaskInput{MilestoneID: &milestone.ID, Title: "逾期任务", ScheduledDate: &yesterday, EstimateMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	todayTask, err := service.CreateTask(t.Context(), userID, draft.ID, CreateTaskInput{MilestoneID: &milestone.ID, Title: "今日任务", ScheduledDate: &today, EstimateMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTask(t.Context(), userID, draft.ID, CreateTaskInput{MilestoneID: &milestone.ID, Title: "未来任务", ScheduledDate: &tomorrow, EstimateMinutes: 45})
	if err != nil {
		t.Fatal(err)
	}
	disposable, err := service.CreateTask(t.Context(), userID, draft.ID, CreateTaskInput{Title: "删除草稿任务", ScheduledDate: &tomorrow})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTask(t.Context(), userID, disposable.ID); err != nil {
		t.Fatal(err)
	}
	var deletedCount int64
	if err := db.Table("tasks").Where("id = ?", disposable.ID).Count(&deletedCount).Error; err != nil || deletedCount != 0 {
		t.Fatalf("draft task was not deleted: count=%d err=%v", deletedCount, err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, draft.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTask(t.Context(), userID, overdue.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("active task deletion should be rejected, got %v", err)
	}
	if _, err := service.GetPlan(t.Context(), otherUserID, calendar.Plan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user plan read = %v", err)
	}

	location, _ := time.LoadLocation("Asia/Shanghai")
	service.now = func() time.Time { return time.Date(2026, 9, 23, 10, 0, 0, 0, location) }
	dashboard, err := service.TodayDashboard(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Overdue) != 1 || len(dashboard.Today) != 1 || len(dashboard.Future) != 1 || len(dashboard.SequencePlans) != 1 || dashboard.SequencePlans[0].NextTask == nil || dashboard.SequencePlans[0].NextTask.ID != sequenceTask.ID {
		t.Fatalf("unexpected dashboard counts: %+v", dashboard)
	}
	if _, err := service.UpdateTask(t.Context(), userID, overdue.ID, UpdateTaskInput{Version: 99, Title: stringPointer("conflict")}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}

	session, err := service.StartSession(t.Context(), userID, todayTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartSession(t.Context(), userID, sequenceTask.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected one-running-session conflict, got %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 23, 11, 0, 0, 0, location) }
	finished, err := service.FinishSession(t.Context(), userID, session.ID, FinishSessionInput{Note: "完成开发"})
	if err != nil || finished.DurationSeconds != 3600 {
		t.Fatalf("finish session=%+v err=%v", finished, err)
	}
	history, err := service.ListTaskSessions(t.Context(), userID, todayTask.ID)
	if err != nil || len(history) != 1 || history[0].Note != "完成开发" {
		t.Fatalf("unexpected session history=%+v err=%v", history, err)
	}
	if _, err := service.ListTaskSessions(t.Context(), otherUserID, todayTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user session history = %v", err)
	}
	if _, err := service.SetTaskStatus(t.Context(), userID, todayTask.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetTaskStatus(t.Context(), userID, todayTask.ID, "complete"); err != nil {
		t.Fatalf("completing an already completed task should be idempotent: %v", err)
	}

	sequenceSession, err := service.StartSession(t.Context(), userID, sequenceTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 23, 11, 30, 0, 0, location) }
	if _, err := service.SetTaskStatus(t.Context(), userID, sequenceTask.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	autoFinished, err := service.ListTaskSessions(t.Context(), userID, sequenceTask.ID)
	if err != nil || len(autoFinished) != 1 || autoFinished[0].ID != sequenceSession.ID || autoFinished[0].Status != "finished" || autoFinished[0].DurationSeconds != 1800 {
		t.Fatalf("task completion did not finish running session: %+v err=%v", autoFinished, err)
	}
	dashboard, err = service.TodayDashboard(t.Context(), userID)
	if err != nil || dashboard.SequencePlans[0].NextTask == nil || dashboard.SequencePlans[0].NextTask.ID != sequenceNext.ID {
		t.Fatalf("sequence plan did not advance: %+v err=%v", dashboard.SequencePlans, err)
	}

	clone, err := service.ClonePlanVersion(t.Context(), userID, calendar.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range clone.Milestones {
		for _, task := range item.Tasks {
			if task.Title == todayTask.Title {
				t.Fatal("completed task was copied into next version")
			}
		}
	}
	if _, err := service.SetTaskStatus(t.Context(), userID, overdue.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, clone.ID); err != nil {
		t.Fatal(err)
	}
	calendarAfterActivation, err := service.GetPlan(t.Context(), userID, calendar.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range calendarAfterActivation.Versions {
		if version.Status != "active" {
			continue
		}
		for _, stage := range version.Milestones {
			for _, task := range stage.Tasks {
				if task.Title == overdue.Title {
					t.Fatal("task completed after draft creation remained in activated draft")
				}
			}
		}
	}

	sequenceClone, err := service.ClonePlanVersion(t.Context(), userID, sequence.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, location) }
	runningSequence, err := service.StartSession(t.Context(), userID, sequenceNext.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, sequenceClone.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected activation to reject a running session, got %v", err)
	}
	if _, err := service.DiscardSession(t.Context(), userID, runningSequence.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, sequenceClone.ID); err != nil {
		t.Fatal(err)
	}

	conflictPlan, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "版本冲突验证", Mode: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	conflictTask, err := service.CreateTask(t.Context(), userID, conflictPlan.Versions[0].ID, CreateTaskInput{Title: "原始结构"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, conflictPlan.Versions[0].ID); err != nil {
		t.Fatal(err)
	}
	conflictDraft, err := service.ClonePlanVersion(t.Context(), userID, conflictPlan.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	changedTitle := "源版本已经改变"
	if _, err := service.UpdateTask(t.Context(), userID, conflictTask.ID, UpdateTaskInput{Version: conflictTask.Version, Title: &changedTitle}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, conflictDraft.ID); !errors.Is(err, ErrPlanVersionConflict) {
		t.Fatalf("expected plan version conflict, got %v", err)
	}

	review, err := service.WeeklyReview(t.Context(), userID, "2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	if review.CompletedTasks != 3 || review.ActualDurationSeconds != 5400 || review.PlannedMinutes != 840 {
		t.Fatalf("unexpected review: %+v", review)
	}
}

func cleanupUser(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	statements := []string{
		"UPDATE plans SET active_version_id = NULL WHERE user_id = ?",
		"UPDATE goals SET parent_goal_id = NULL WHERE user_id = ?",
		"DELETE e FROM task_schedule_events e WHERE e.user_id = ?",
		"DELETE s FROM study_sessions s WHERE s.user_id = ?",
		"DELETE t FROM tasks t WHERE t.user_id = ?",
		"DELETE m FROM milestones m WHERE m.user_id = ?",
		"DELETE pv FROM plan_versions pv WHERE pv.user_id = ?",
		"DELETE p FROM plans p WHERE p.user_id = ?",
		"DELETE g FROM goals g WHERE g.user_id = ?",
		"DELETE up FROM user_preferences up WHERE up.user_id = ?",
	}
	for _, statement := range statements {
		if err := db.Exec(statement, userID).Error; err != nil {
			t.Logf("cleanup: %v", err)
		}
	}
}

func stringPointer(value string) *string { return &value }
