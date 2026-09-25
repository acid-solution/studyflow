-- +goose Up
-- A task that had ever been rescheduled could not be deleted. The schedule events
-- pointed at it with ON DELETE RESTRICT and nothing in the application ever
-- removed them, so DELETE /api/v1/tasks/:id — and DeleteMilestone, which deletes
-- its tasks first, and activating a version, which purges draft tasks — failed
-- with MySQL error 1451 and surfaced as a 500. The row was permanently stuck.
--
-- The events only describe one task's date history and the weekly review already
-- joins through tasks to count them, so letting them go with the task loses
-- nothing that was reachable before.
ALTER TABLE task_schedule_events DROP FOREIGN KEY fk_schedule_events_task;
ALTER TABLE task_schedule_events ADD CONSTRAINT fk_schedule_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE task_schedule_events DROP FOREIGN KEY fk_schedule_events_task;
ALTER TABLE task_schedule_events ADD CONSTRAINT fk_schedule_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE RESTRICT;
