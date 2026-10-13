package studyflow

import (
	"context"
	"time"
)

func (s *Service) GetTask(ctx context.Context, userID, taskID string) (*TaskView, error) {
	task, err := s.repo.Task(ctx, userID, taskID, false)
	if err != nil {
		return nil, err
	}
	version, plan, err := s.versionAndPlan(ctx, s.repo, userID, task.PlanVersionID, false)
	if err != nil {
		return nil, err
	}
	pref, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(pref.Timezone)
	if err != nil {
		return nil, err
	}
	view := taskView(*task, plan.ID, plan.Title, version.Mode, dateOnly(s.now().In(location)))
	view.PlanStatus, view.VersionStatus = plan.Status, version.Status
	return &view, nil
}
