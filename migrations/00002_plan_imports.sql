-- +goose Up
CREATE TABLE plan_imports (
    id CHAR(36) NOT NULL,
    user_id CHAR(36) NOT NULL,
    idempotency_key VARCHAR(200) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_digest CHAR(64) NOT NULL,
    plan_id CHAR(36) NOT NULL,
    plan_version_id CHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_plan_imports_user_key (user_id, idempotency_key),
    KEY idx_plan_imports_plan (plan_id),
    KEY idx_plan_imports_version (plan_version_id),
    CONSTRAINT fk_plan_imports_plan FOREIGN KEY (plan_id) REFERENCES plans(id) ON DELETE CASCADE,
    CONSTRAINT fk_plan_imports_version FOREIGN KEY (plan_version_id) REFERENCES plan_versions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS plan_imports;
