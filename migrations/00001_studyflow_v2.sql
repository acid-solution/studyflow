-- +goose Up
CREATE TABLE goals (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    parent_goal_id CHAR(36) NULL,
    title VARCHAR(160) NOT NULL,
    description TEXT NOT NULL,
    success_criteria TEXT NOT NULL,
    target_date DATE NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    version INT UNSIGNED NOT NULL DEFAULT 1,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_goals_user_parent (user_id, parent_goal_id),
    KEY idx_goals_user_status (user_id, status),
    CONSTRAINT fk_goals_parent FOREIGN KEY (parent_goal_id) REFERENCES goals(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE plans (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    goal_id CHAR(36) NOT NULL,
    active_version_id CHAR(36) NULL,
    title VARCHAR(160) NOT NULL,
    description TEXT NOT NULL,
    mode VARCHAR(16) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_plans_user_goal (user_id, goal_id),
    CONSTRAINT fk_plans_goal FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE plan_versions (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    plan_id CHAR(36) NOT NULL,
    version_no INT UNSIGNED NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    weekly_capacity_minutes INT UNSIGNED NOT NULL DEFAULT 0,
    start_date DATE NULL,
    end_date DATE NULL,
    base_version_id CHAR(36) NULL,
    base_structure_revision INT UNSIGNED NOT NULL DEFAULT 0,
    structure_revision INT UNSIGNED NOT NULL DEFAULT 1,
    activated_at DATETIME(6) NULL,
    superseded_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_plan_versions_number (plan_id, version_no),
    KEY idx_plan_versions_user_status (user_id, status),
    CONSTRAINT fk_plan_versions_plan FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE CASCADE,
    CONSTRAINT fk_plan_versions_base FOREIGN KEY (base_version_id) REFERENCES plan_versions(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE plans
    ADD CONSTRAINT fk_plans_active_version FOREIGN KEY (active_version_id) REFERENCES plan_versions(id) ON DELETE SET NULL;

CREATE TABLE milestones (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    plan_version_id CHAR(36) NOT NULL,
    title VARCHAR(160) NOT NULL,
    outcome TEXT NOT NULL,
    position INT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_milestones_position (plan_version_id, position),
    KEY idx_milestones_user_version (user_id, plan_version_id),
    CONSTRAINT fk_milestones_version FOREIGN KEY (plan_version_id) REFERENCES plan_versions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE tasks (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    plan_version_id CHAR(36) NOT NULL,
    milestone_id CHAR(36) NULL,
    copied_from_task_id CHAR(36) NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    estimate_minutes INT UNSIGNED NOT NULL DEFAULT 0,
    scheduled_date DATE NULL,
    position INT UNSIGNED NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'todo',
    version INT UNSIGNED NOT NULL DEFAULT 1,
    completed_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_tasks_user_status_date (user_id, status, scheduled_date),
    KEY idx_tasks_version_position (plan_version_id, position),
    KEY idx_tasks_milestone_position (milestone_id, position),
    CONSTRAINT fk_tasks_version FOREIGN KEY (plan_version_id) REFERENCES plan_versions(id) ON DELETE CASCADE,
    CONSTRAINT fk_tasks_milestone FOREIGN KEY (milestone_id) REFERENCES milestones(id) ON DELETE SET NULL,
    CONSTRAINT fk_tasks_source FOREIGN KEY (copied_from_task_id) REFERENCES tasks(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE study_sessions (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    task_id CHAR(36) NOT NULL,
    started_at DATETIME(6) NOT NULL,
    ended_at DATETIME(6) NULL,
    duration_seconds BIGINT UNSIGNED NOT NULL DEFAULT 0,
    note TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'running',
    running_slot TINYINT UNSIGNED NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_sessions_one_running (user_id, running_slot),
    KEY idx_sessions_user_started (user_id, started_at),
    KEY idx_sessions_task (task_id),
    CONSTRAINT fk_sessions_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE task_schedule_events (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    task_id CHAR(36) NOT NULL,
    old_scheduled_date DATE NULL,
    new_scheduled_date DATE NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY idx_schedule_events_user_created (user_id, created_at),
    KEY idx_schedule_events_task (task_id),
    CONSTRAINT fk_schedule_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_preferences (
    user_id CHAR(36) NOT NULL,
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    week_start VARCHAR(16) NOT NULL DEFAULT 'monday',
    default_plan_mode VARCHAR(16) NOT NULL DEFAULT 'calendar',
    show_completed_today BOOLEAN NOT NULL DEFAULT TRUE,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS task_schedule_events;
DROP TABLE IF EXISTS study_sessions;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS milestones;
ALTER TABLE plans DROP FOREIGN KEY fk_plans_active_version;
DROP TABLE IF EXISTS plan_versions;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS goals;
DROP TABLE IF EXISTS user_preferences;
