-- +goose Up
ALTER TABLE goals
    ADD COLUMN achieved_at DATETIME(6) NULL,
    ADD COLUMN excluded_from_parent_completion BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE plans
    ADD COLUMN objective TEXT NULL,
    ADD COLUMN success_criteria TEXT NULL,
    ADD COLUMN target_date DATE NULL,
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active',
    ADD COLUMN revision INT UNSIGNED NOT NULL DEFAULT 1,
    ADD COLUMN achieved_at DATETIME(6) NULL,
    ADD COLUMN excluded_from_goal_completion BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE plans SET objective = title, success_criteria = '';

ALTER TABLE plans
    MODIFY COLUMN objective TEXT NOT NULL,
    MODIFY COLUMN success_criteria TEXT NOT NULL,
    ADD KEY idx_plans_user_status (user_id, status);

ALTER TABLE plan_versions ADD COLUMN mode VARCHAR(16) NULL AFTER status;

UPDATE plan_versions pv
JOIN plans p ON p.id = pv.plan_id
SET pv.mode = p.mode;

ALTER TABLE plan_versions MODIFY COLUMN mode VARCHAR(16) NOT NULL;
ALTER TABLE plans DROP COLUMN mode;

-- +goose Down
ALTER TABLE plans ADD COLUMN mode VARCHAR(16) NULL AFTER description;

UPDATE plans p
SET p.mode = COALESCE(
    (SELECT active_version.mode FROM plan_versions active_version WHERE active_version.id = p.active_version_id),
    (SELECT latest_version.mode FROM plan_versions latest_version WHERE latest_version.plan_id = p.id ORDER BY latest_version.version_no DESC LIMIT 1),
    'calendar'
);

ALTER TABLE plans MODIFY COLUMN mode VARCHAR(16) NOT NULL;
ALTER TABLE plan_versions DROP COLUMN mode;

ALTER TABLE plans
    DROP KEY idx_plans_user_status,
    DROP COLUMN excluded_from_goal_completion,
    DROP COLUMN achieved_at,
    DROP COLUMN revision,
    DROP COLUMN status,
    DROP COLUMN target_date,
    DROP COLUMN success_criteria,
    DROP COLUMN objective;

ALTER TABLE goals
    DROP COLUMN excluded_from_parent_completion,
    DROP COLUMN achieved_at;
