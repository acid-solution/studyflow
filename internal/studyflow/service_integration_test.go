package studyflow

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"studyflow/internal/database"
	"studyflow/internal/model"

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

	calendar, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "StudyFlow 开发", Objective: "完成 StudyFlow 第一版", Mode: "calendar", WeeklyCapacityMinutes: 600})
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "Go 八股", Objective: "能够解释常见 Go 后端问题", Mode: "sequence", WeeklyCapacityMinutes: 240})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := service.ListPlans(t.Context(), userID, child.ID, "", "")
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
	// Finished work gets its own section, and today holds only what is still open,
	// so the same task never appears in both.
	afterFinish, err := service.TodayDashboard(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFinish.CompletedToday) != 1 || afterFinish.CompletedToday[0].ID != todayTask.ID {
		t.Fatalf("completed today should list the finished task: %+v", afterFinish.CompletedToday)
	}
	for _, task := range afterFinish.Today {
		if task.Status == model.TaskStatusDone {
			t.Fatalf("today should hold only open tasks, got %+v", task)
		}
	}
	// A task due yesterday and finished today belongs in the finished section even
	// though its scheduled date is in the past.
	completedOverdue, err := service.SetTaskStatus(t.Context(), userID, overdue.ID, "complete")
	if err != nil {
		t.Fatal(err)
	}
	afterLateFinish, err := service.TodayDashboard(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	lateFinished := false
	for _, task := range afterLateFinish.CompletedToday {
		if task.ID == overdue.ID {
			lateFinished = true
		}
	}
	if !lateFinished {
		t.Fatalf("a task finished today is finished today even if it was due earlier: %+v", afterLateFinish.CompletedToday)
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

	sourceCalendarVersionID := draft.ID
	clone, err := service.CreatePlanVersion(t.Context(), userID, calendar.Plan.ID, CreatePlanVersionInput{CreationMode: "copy", SourceVersionID: &sourceCalendarVersionID})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range clone.Milestones {
		for _, task := range item.Tasks {
			if task.Title == todayTask.Title && task.Status != model.TaskStatusDone {
				t.Fatal("completed task should be copied with its status")
			}
		}
	}
	renamedAfterCopy := "来源版本后来改名"
	if _, err := service.UpdateTask(t.Context(), userID, overdue.ID, UpdateTaskInput{Version: completedOverdue.Version, Title: &renamedAfterCopy}); err != nil {
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
				if task.Title == renamedAfterCopy {
					t.Fatal("source changes after copy must not alter the independent draft")
				}
			}
		}
	}

	sourceSequenceVersionID := sequenceDraft.ID
	sequenceClone, err := service.CreatePlanVersion(t.Context(), userID, sequence.Plan.ID, CreatePlanVersionInput{CreationMode: "copy", SourceVersionID: &sourceSequenceVersionID})
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

	conflictPlan, err := service.CreatePlan(t.Context(), userID, child.ID, CreatePlanInput{Title: "版本独立性验证", Objective: "验证版本互不联动", Mode: "sequence"})
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
	conflictSourceID := conflictPlan.Versions[0].ID
	conflictDraft, err := service.CreatePlanVersion(t.Context(), userID, conflictPlan.Plan.ID, CreatePlanVersionInput{CreationMode: "copy", SourceVersionID: &conflictSourceID})
	if err != nil {
		t.Fatal(err)
	}
	changedTitle := "源版本已经改变"
	if _, err := service.UpdateTask(t.Context(), userID, conflictTask.ID, UpdateTaskInput{Version: conflictTask.Version, Title: &changedTitle}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(t.Context(), userID, conflictDraft.ID); err != nil {
		t.Fatalf("source changes must not block independent draft activation: %v", err)
	}

	review, err := service.WeeklyReview(t.Context(), userID, "2026-09-21")
	if err != nil {
		t.Fatal(err)
	}
	if review.CompletedTasks != 3 || review.ActualDurationSeconds != 5400 || review.PlannedMinutes != 840 {
		t.Fatalf("unexpected review: %+v", review)
	}
}

func TestGoalPlanSemanticsAndIndependentVersionsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	userID := uuid.NewString()
	otherUserID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	defer cleanupUser(t, db, otherUserID)
	ctx := t.Context()

	empty, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "空目标"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteGoal(ctx, userID, empty.ID, CompletionInput{AcknowledgeIncomplete: true}); !errors.Is(err, ErrNoRequiredChildren) {
		t.Fatalf("an empty goal cannot be completed, got %v", err)
	}

	root, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "成为可靠的后端开发者"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateGoal(ctx, userID, CreateGoalInput{ParentGoalID: &root.ID, Title: "掌握鉴权"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePlan(ctx, userID, child.ID, CreatePlanInput{Title: "缺少目标", Mode: model.PlanModeCalendar}); !errors.Is(err, ErrValidation) {
		t.Fatalf("a plan objective is required, got %v", err)
	}
	plan, err := service.CreatePlan(ctx, userID, child.ID, CreatePlanInput{
		Title:           "鉴权系统实践",
		Objective:       "能够独立实现并解释完整鉴权系统",
		SuccessCriteria: "通过关键失败场景测试",
		Mode:            model.PlanModeCalendar,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompletePlan(ctx, userID, plan.Plan.ID, CompletionInput{}); err == nil {
		t.Fatal("a plan without an active version should require confirmation")
	} else {
		var confirmation *CompletionConfirmationError
		if !errors.As(err, &confirmation) {
			t.Fatalf("expected completion confirmation, got %v", err)
		}
	}

	v1 := plan.Versions[0]
	doneTask, err := service.CreateTask(ctx, userID, v1.ID, CreateTaskInput{Title: "实现登录"})
	if err != nil {
		t.Fatal(err)
	}
	openTask, err := service.CreateTask(ctx, userID, v1.ID, CreateTaskInput{Title: "实现刷新轮换"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, v1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetTaskStatus(ctx, userID, doneTask.ID, "complete"); err != nil {
		t.Fatal(err)
	}
	copyV2, err := service.CreatePlanVersion(ctx, userID, plan.Plan.ID, CreatePlanVersionInput{CreationMode: "copy", SourceVersionID: &v1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if copyV2.Mode != model.PlanModeCalendar || copyV2.SourceVersionID == nil || *copyV2.SourceVersionID != v1.ID {
		t.Fatalf("copy should retain mode and source trace: %+v", copyV2)
	}
	var copiedDone, copiedOpen *TaskView
	for _, task := range copyV2.UnassignedTasks {
		switch task.Title {
		case doneTask.Title:
			value := task
			copiedDone = &value
		case openTask.Title:
			value := task
			copiedOpen = &value
		}
	}
	if copiedDone == nil || copiedDone.Status != model.TaskStatusDone || copiedDone.CompletedAt != nil || copiedOpen == nil || copiedOpen.Status != model.TaskStatusTodo {
		t.Fatalf("copy should preserve status without copying completion events: %+v", copyV2.UnassignedTasks)
	}
	if _, err := service.CreatePlanVersion(ctx, userID, plan.Plan.ID, CreatePlanVersionInput{CreationMode: "blank", Mode: model.PlanModeSequence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("only one draft is allowed, got %v", err)
	}
	newTitle := "来源版本后来改名"
	if _, err := service.UpdateTask(ctx, userID, openTask.ID, UpdateTaskInput{Version: openTask.Version, Title: &newTitle}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, copyV2.ID); err != nil {
		t.Fatalf("source changes must not block activating an independent copy: %v", err)
	}
	detail, err := service.GetPlan(ctx, userID, plan.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeV2 := detail.Versions[0]
	for _, version := range detail.Versions {
		if version.ID == copyV2.ID {
			activeV2 = version
		}
	}
	for _, task := range activeV2.UnassignedTasks {
		if task.ID == copiedOpen.ID && task.Title != openTask.Title {
			t.Fatalf("source modification leaked into copied version: %+v", task)
		}
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, v1.ID); err != nil {
		t.Fatalf("a historical version should be switchable: %v", err)
	}
	blank, err := service.CreatePlanVersion(ctx, userID, plan.Plan.ID, CreatePlanVersionInput{CreationMode: "blank", Mode: model.PlanModeSequence, WeeklyCapacityMinutes: 180})
	if err != nil {
		t.Fatal(err)
	}
	if blank.VersionNo != 3 || blank.Mode != model.PlanModeSequence || len(blank.UnassignedTasks) != 0 || len(blank.Milestones) != 0 {
		t.Fatalf("blank version should be independent and use max version + 1: %+v", blank)
	}
	if _, err := service.CancelPlanVersion(ctx, userID, blank.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivatePlanVersion(ctx, userID, blank.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("a canceled version cannot be activated, got %v", err)
	}

	if _, err := service.CompletePlan(ctx, userID, plan.Plan.ID, CompletionInput{}); err == nil {
		t.Fatal("open tasks should require explicit acknowledgement")
	}
	if _, err := service.CompletePlan(ctx, userID, plan.Plan.ID, CompletionInput{AcknowledgeIncomplete: true}); err != nil {
		t.Fatal(err)
	}
	dashboard, err := service.TodayDashboard(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range append(append(dashboard.Overdue, dashboard.Today...), dashboard.Future...) {
		if task.PlanID == plan.Plan.ID {
			t.Fatal("an achieved plan must not appear on the dashboard")
		}
	}

	if _, err := service.AbandonGoal(ctx, userID, child.ID, AbandonInput{ParentEffect: "block"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReopenPlan(ctx, userID, plan.Plan.ID); err == nil {
		t.Fatal("a plan cannot reopen while an ancestor goal is inactive")
	} else {
		var ancestors *AncestorStateError
		if !errors.As(err, &ancestors) || len(ancestors.Ancestors) == 0 {
			t.Fatalf("expected inactive ancestor details, got %v", err)
		}
	}
	if _, err := service.UpdateGoalCompletionPolicy(ctx, userID, child.ID, CompletionPolicyInput{ParentEffect: "exclude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateGoalCompletionPolicy(ctx, otherUserID, child.ID, CompletionPolicyInput{ParentEffect: "exclude"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user's goal policy must look missing, got %v", err)
	}
	tree, err := service.GoalTree(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	var rootView *GoalNode
	for _, candidate := range tree {
		if candidate.ID == root.ID {
			rootView = candidate
		}
	}
	if rootView == nil || rootView.CompletionSummary.IncludedChildren != 0 || rootView.CompletionSummary.ExcludedChildren != 1 {
		t.Fatalf("excluded child should leave the parent without required children: %+v", rootView)
	}
	if _, err := service.ReopenGoal(ctx, userID, child.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReopenPlan(ctx, userID, plan.Plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AbandonPlan(ctx, userID, plan.Plan.ID, AbandonInput{ParentEffect: "block"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePlanCompletionPolicy(ctx, userID, plan.Plan.ID, CompletionPolicyInput{ParentEffect: "exclude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePlanCompletionPolicy(ctx, otherUserID, plan.Plan.ID, CompletionPolicyInput{ParentEffect: "exclude"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user's plan policy must look missing, got %v", err)
	}
	childView, err := service.goalByID(ctx, userID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if childView.CompletionSummary.IncludedChildren != 0 || childView.CompletionSummary.ExcludedChildren != 1 {
		t.Fatalf("plan exclusion policy was not reflected in its goal: %+v", childView.CompletionSummary)
	}
}

func TestPlanTreeImportIntegration(t *testing.T) {
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
	defer cleanupUser(t, db, otherUserID)

	clock := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	ctx := t.Context()

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "导入目标", SuccessCriteria: "完成一次批量导入"})
	if err != nil {
		t.Fatal(err)
	}

	// Guard the extraction this change made: creating a single milestone or task
	// must still bump the structure revision of the version it writes into.
	guard, err := service.CreatePlan(ctx, userID, goal.ID, CreatePlanInput{Title: "护栏计划", Objective: "验证结构修订", Mode: model.PlanModeCalendar, WeeklyCapacityMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	if guard.Versions[0].StructureRevision != 1 {
		t.Fatalf("a new version should start at revision 1, got %d", guard.Versions[0].StructureRevision)
	}
	if _, err := service.CreateMilestone(ctx, userID, guard.Versions[0].ID, CreateMilestoneInput{Title: "护栏阶段"}); err != nil {
		t.Fatal(err)
	}
	assertStructureRevision(t, service, userID, guard.Plan.ID, 2)
	if _, err := service.CreateTask(ctx, userID, guard.Versions[0].ID, CreateTaskInput{Title: "护栏任务"}); err != nil {
		t.Fatal(err)
	}
	assertStructureRevision(t, service, userID, guard.Plan.ID, 3)

	const importTitle = "导入的复习计划"
	payload := func() ImportPlanTreeInput {
		return ImportPlanTreeInput{
			GoalID:                goal.ID,
			Title:                 importTitle,
			Description:           "由批量导入创建。",
			Objective:             "完成一轮系统复习",
			Mode:                  model.PlanModeCalendar,
			WeeklyCapacityMinutes: 300,
			Milestones: []ImportMilestoneInput{
				{Title: "第一阶段", Outcome: "打基础", Tasks: []ImportTaskInput{
					{Title: "任务 A", Description: "描述 A", EstimateMinutes: 30, ScheduledDate: stringPointer("2026-03-03")},
					{Title: "任务 B", EstimateMinutes: 40},
				}},
				{Title: "第二阶段", Tasks: []ImportTaskInput{
					{Title: "任务 C", EstimateMinutes: 50},
				}},
			},
			Tasks: []ImportTaskInput{{Title: "未分阶段任务", EstimateMinutes: 20}},
		}
	}
	const key = "import-key-happy-path"

	// 1. Happy path.
	first, err := service.ImportPlanTree(ctx, userID, key, payload())
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatal("the first import must not be reported as a replay")
	}
	if len(first.Plan.Versions) != 1 {
		t.Fatalf("want exactly one version, got %d", len(first.Plan.Versions))
	}
	version := first.Plan.Versions[0]
	if version.VersionNo != 1 || version.Status != model.PlanVersionDraft || version.StructureRevision != 1 {
		t.Fatalf("unexpected first version: %+v", version)
	}
	if len(version.Milestones) != 2 {
		t.Fatalf("want 2 milestones, got %d", len(version.Milestones))
	}
	for index, want := range []string{"第一阶段", "第二阶段"} {
		if version.Milestones[index].Title != want || version.Milestones[index].Position != uint(index+1) {
			t.Fatalf("milestone %d out of order: %+v", index, version.Milestones[index])
		}
	}
	if len(version.Milestones[0].Tasks) != 2 || len(version.Milestones[1].Tasks) != 1 {
		t.Fatalf("unexpected task counts: %d and %d", len(version.Milestones[0].Tasks), len(version.Milestones[1].Tasks))
	}
	for index, task := range version.Milestones[0].Tasks {
		if task.Position != uint(index+1) || task.Status != model.TaskStatusTodo || task.Version != 1 {
			t.Fatalf("task %d not written as a fresh todo: %+v", index, task)
		}
	}
	if len(version.UnassignedTasks) != 1 || version.UnassignedTasks[0].MilestoneID != nil {
		t.Fatalf("unexpected unassigned tasks: %+v", version.UnassignedTasks)
	}
	// An imported plan and everything under it is marked as agent-originated, so
	// the app can tell it apart from what the user made by hand.
	if first.Plan.Plan.Source != model.SourceAgent {
		t.Fatalf("an imported plan should be marked agent, got %q", first.Plan.Plan.Source)
	}
	for _, milestone := range version.Milestones {
		for _, task := range milestone.Tasks {
			if task.Source != model.SourceAgent {
				t.Fatalf("an imported task should be marked agent: %+v", task)
			}
		}
	}
	for _, task := range version.UnassignedTasks {
		if task.Source != model.SourceAgent {
			t.Fatalf("an imported unassigned task should be marked agent: %+v", task)
		}
	}
	// The guard plan was created through the ordinary endpoint, so it stays user.
	if guard.Plan.Source != model.SourceUser {
		t.Fatalf("a hand-made plan should be marked user, got %q", guard.Plan.Source)
	}
	var receipt model.PlanImport
	if err := db.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	if receipt.PlanID != first.PlanID || receipt.PlanVersionID != first.PlanVersionID {
		t.Fatalf("receipt does not point at the created tree: %+v", receipt)
	}
	if len(receipt.RequestDigest) != 64 {
		t.Fatalf("want a sha256 hex digest, got %q", receipt.RequestDigest)
	}
	if !receipt.CreatedAt.UTC().Equal(clock) {
		t.Fatalf("receipt timestamp should come from the service clock: %s", receipt.CreatedAt)
	}
	plansAfterFirst := countPlansByTitle(t, db, userID, importTitle)

	// 2. A byte-identical retry replays instead of creating a second tree.
	second, err := service.ImportPlanTree(ctx, userID, key, payload())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("a repeated request must be reported as a replay")
	}
	if second.ImportID != first.ImportID || second.PlanID != first.PlanID || second.PlanVersionID != first.PlanVersionID {
		t.Fatalf("replay returned a different tree: %+v", second)
	}
	if got := countPlansByTitle(t, db, userID, importTitle); got != plansAfterFirst {
		t.Fatalf("replay created extra plans: %d -> %d", plansAfterFirst, got)
	}

	// 3. A retry that is spelled differently but means the same thing must also
	// replay. This only holds because the digest covers the normalized payload.
	spelled := payload()
	spelled.Title = "  导入的复习计划  "
	spelled.Description = "\t由批量导入创建。 "
	spelled.Milestones[0].Tasks[1].ScheduledDate = stringPointer("   ")
	spelled.Milestones[0].Outcome = " 打基础 "
	third, err := service.ImportPlanTree(ctx, userID, key, spelled)
	if err != nil {
		t.Fatalf("a semantically identical retry must replay, got %v", err)
	}
	if !third.Replayed {
		t.Fatal("a semantically identical retry must be reported as a replay")
	}

	// 3b. The receipts double as the audit trail: exactly one row per import that
	// actually created something, describing what and when.
	history, err := service.ListPlanImports(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("want exactly one audit row after one import, got %d", len(history))
	}
	entry := history[0]
	if entry.ImportID != first.ImportID || entry.PlanID != first.PlanID || entry.IdempotencyKey != key {
		t.Fatalf("audit row does not match the import: %+v", entry)
	}
	if entry.PlanTitle != importTitle || entry.Milestones != 2 || entry.Tasks != 4 {
		t.Fatalf("audit row should describe what was created: %+v", entry)
	}
	if !entry.CreatedAt.UTC().Equal(clock) {
		t.Fatalf("audit row should carry the import time: %s", entry.CreatedAt)
	}
	// Neither a replay nor another account's import may show up here.
	if _, err := service.ImportPlanTree(ctx, userID, key, payload()); err != nil {
		t.Fatal(err)
	}
	if otherHistory, err := service.ListPlanImports(ctx, otherUserID); err != nil {
		t.Fatal(err)
	} else if len(otherHistory) != 0 {
		t.Fatalf("another user's history must stay empty, got %d", len(otherHistory))
	}
	if again, err := service.ListPlanImports(ctx, userID); err != nil {
		t.Fatal(err)
	} else if len(again) != 1 {
		t.Fatalf("a replay must not add an audit row, got %d", len(again))
	}

	// 4. The same key with a different payload is a conflict.
	different := payload()
	different.Title = "完全不同的计划"
	if _, err := service.ImportPlanTree(ctx, userID, key, different); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
	differentObjective := payload()
	differentObjective.Objective = "另一项计划目标"
	if _, err := service.ImportPlanTree(ctx, userID, key, differentObjective); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changing the objective must change the idempotency digest, got %v", err)
	}
	if got := countPlansByTitle(t, db, userID, importTitle); got != plansAfterFirst {
		t.Fatalf("a conflicting request must not create anything: %d -> %d", plansAfterFirst, got)
	}

	// 5. A failure part-way through the batch rolls back everything already
	// written and releases the key.
	broken := payload()
	broken.Milestones[1].Tasks[0].Title = "   "
	before := map[string]int64{}
	for _, table := range []string{"plans", "plan_versions", "milestones", "tasks", "plan_imports"} {
		before[table] = countRows(t, db, table, userID)
	}
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-rollback", broken); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	for table, want := range before {
		if got := countRows(t, db, table, userID); got != want {
			t.Fatalf("rolled back import left rows in %s: %d -> %d", table, want, got)
		}
	}
	fixed := payload()
	fixed.Title = "重试后的计划"
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-rollback", fixed); err != nil {
		t.Fatalf("the key should be free after a rollback, got %v", err)
	}

	// 6. An unknown goal fails without claiming the key.
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-unknown-goal", ImportPlanTreeInput{GoalID: uuid.NewString(), Title: "无主计划", Objective: "验证目标归属", Mode: model.PlanModeCalendar}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if got := countRowsForKey(t, db, "import-key-unknown-goal"); got != 0 {
		t.Fatalf("a rejected import must not leave a receipt, got %d", got)
	}

	// 7. The key is scoped to the user, so another account can reuse it.
	otherGoal, err := service.CreateGoal(ctx, otherUserID, CreateGoalInput{Title: "另一个人的目标"})
	if err != nil {
		t.Fatal(err)
	}
	otherPayload := payload()
	otherPayload.GoalID = otherGoal.ID
	other, err := service.ImportPlanTree(ctx, otherUserID, key, otherPayload)
	if err != nil {
		t.Fatalf("the same key must work for another user, got %v", err)
	}
	if other.PlanID == first.PlanID {
		t.Fatal("another user's import must produce its own plan")
	}
	otherReplay, err := service.ImportPlanTree(ctx, otherUserID, key, otherPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !otherReplay.Replayed || otherReplay.PlanID != other.PlanID {
		t.Fatalf("each user should replay their own plan: %+v", otherReplay)
	}

	// 8. A sequence plan cannot carry a date.
	sequence := ImportPlanTreeInput{
		GoalID: goal.ID, Title: "按课时的导入", Objective: "按顺序完成全部课时", Mode: model.PlanModeSequence,
		Milestones: []ImportMilestoneInput{{Title: "课时阶段", Tasks: []ImportTaskInput{
			{Title: "第一课", ScheduledDate: stringPointer("2026-03-05")},
		}}},
	}
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-sequence-bad", sequence); !errors.Is(err, ErrValidation) {
		t.Fatalf("a sequence plan with a date should be rejected, got %v", err)
	}
	sequence.Milestones[0].Tasks[0].ScheduledDate = nil
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-sequence-ok", sequence); err != nil {
		t.Fatalf("a sequence plan without dates should import, got %v", err)
	}

	// 9. Two identical requests at the same time: one creates, the other replays.
	var wait sync.WaitGroup
	start := make(chan struct{})
	results := make([]*ImportPlanTreeResult, 2)
	failures := make([]error, 2)
	for slot := 0; slot < 2; slot++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], failures[index] = service.ImportPlanTree(ctx, userID, "import-key-concurrent", payload())
		}(slot)
	}
	close(start)
	wait.Wait()
	for index, failure := range failures {
		if failure != nil {
			t.Fatalf("concurrent request %d failed: %v", index, failure)
		}
	}
	if results[0].PlanID != results[1].PlanID {
		t.Fatalf("concurrent requests produced different plans: %s and %s", results[0].PlanID, results[1].PlanID)
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("exactly one concurrent request should have created the plan: %+v", results)
	}
	if got := countRowsForKey(t, db, "import-key-concurrent"); got != 1 {
		t.Fatalf("want exactly one receipt, got %d", got)
	}

	// 10. Bounds are enforced before the key is claimed.
	oversized := payload()
	oversized.Milestones = []ImportMilestoneInput{{Title: "超大批次"}}
	for index := 0; index <= importMaxTasks; index++ {
		oversized.Milestones[0].Tasks = append(oversized.Milestones[0].Tasks, ImportTaskInput{Title: "任务"})
	}
	if _, err := service.ImportPlanTree(ctx, userID, "import-key-oversized", oversized); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	if got := countRowsForKey(t, db, "import-key-oversized"); got != 0 {
		t.Fatalf("an oversized request must not claim the key, got %d receipts", got)
	}
}

// A task that had been rescheduled used to be undeletable: the schedule-event
// foreign key was ON DELETE RESTRICT and nothing ever removed the events, so
// deleting the task failed with MySQL 1451 and surfaced as a 500.
func TestRescheduledTaskCanBeDeletedIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: "删除目标"})
	if err != nil {
		t.Fatal(err)
	}
	version := draftVersion(t, service, ctx, userID, goal.ID, "删除计划")
	task, err := service.CreateTask(ctx, userID, version, CreateTaskInput{Title: "待删任务", ScheduledDate: stringPointer("2026-05-01")})
	if err != nil {
		t.Fatal(err)
	}
	// Rescheduling writes a task_schedule_events row pointing at the task.
	if _, err := service.UpdateTask(ctx, userID, task.ID, UpdateTaskInput{Version: task.Version, ScheduledDate: stringPointer("2026-05-08")}); err != nil {
		t.Fatal(err)
	}
	var events int64
	if err := db.Model(&model.TaskScheduleEvent{}).Where("task_id = ?", task.ID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("rescheduling should have written a schedule event")
	}

	if err := service.DeleteTask(ctx, userID, task.ID); err != nil {
		t.Fatalf("a rescheduled task must still be deletable: %v", err)
	}
	if err := db.Model(&model.TaskScheduleEvent{}).Where("task_id = ?", task.ID).Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("the events should have gone with the task, %d left", events)
	}
}

// Title limits are character limits: varchar counts characters, so a CJK title
// that fits the column must be accepted, and the import path has to agree with
// the ordinary one.
func TestTitleLimitsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	db, err := database.OpenMySQL(dsn, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db)
	userID := uuid.NewString()
	defer cleanupUser(t, db, userID)
	ctx := t.Context()

	atLimit := strings.Repeat("汉", 160)
	goal, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: atLimit})
	if err != nil {
		t.Fatalf("160 CJK characters fit varchar(160): %v", err)
	}
	if _, err := service.CreateGoal(ctx, userID, CreateGoalInput{Title: atLimit + "汉"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("161 characters should be rejected, got %v", err)
	}
	// The import path has to accept exactly the same titles.
	if _, err := service.ImportPlanTree(ctx, userID, "title-limit-key", ImportPlanTreeInput{
		GoalID: goal.ID, Title: atLimit, Objective: "验证标题边界", Mode: model.PlanModeCalendar,
	}); err != nil {
		t.Fatalf("the import must accept what the ordinary path accepts: %v", err)
	}
	if _, err := service.ImportPlanTree(ctx, userID, "title-limit-key-2", ImportPlanTreeInput{
		GoalID: goal.ID, Title: atLimit + "汉", Objective: "验证标题边界", Mode: model.PlanModeCalendar,
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("an over-long import title should be rejected, got %v", err)
	}
}

func assertStructureRevision(t *testing.T, service *Service, userID, planID string, want uint) {
	t.Helper()
	detail, err := service.GetPlan(t.Context(), userID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Versions[0].StructureRevision != want {
		t.Fatalf("structure revision: want %d, got %d", want, detail.Versions[0].StructureRevision)
	}
}

func countRows(t *testing.T, db *gorm.DB, table, userID string) int64 {
	t.Helper()
	var count int64
	if err := db.Table(table).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func countPlansByTitle(t *testing.T, db *gorm.DB, userID, title string) int64 {
	t.Helper()
	var count int64
	if err := db.Table("plans").Where("user_id = ? AND title = ?", userID, title).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func countRowsForKey(t *testing.T, db *gorm.DB, key string) int64 {
	t.Helper()
	var count int64
	if err := db.Table("plan_imports").Where("idempotency_key = ?", key).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func cleanupUser(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	statements := []string{
		"UPDATE plans SET active_version_id = NULL WHERE user_id = ?",
		"UPDATE goals SET parent_goal_id = NULL WHERE user_id = ?",
		"DELETE pi FROM plan_imports pi WHERE pi.user_id = ?",
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
