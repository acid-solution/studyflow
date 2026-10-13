-- +goose Up
CREATE TABLE learning_route_bindings (
 id CHAR(36) PRIMARY KEY, user_id CHAR(36) NOT NULL, source_route_id CHAR(36) NOT NULL,
 goal_id CHAR(36) NULL, last_source_version BIGINT UNSIGNED NOT NULL DEFAULT 0,
 plan_refs JSON NOT NULL,
 UNIQUE KEY uk_route_owner (user_id,source_route_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE learning_route_imports (
 id CHAR(36) PRIMARY KEY,user_id CHAR(36) NOT NULL,idempotency_key VARCHAR(200) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 source_route_id CHAR(36) NOT NULL,source_version BIGINT UNSIGNED NOT NULL,request_digest CHAR(64) NOT NULL,
 receipt JSON NOT NULL,created_at DATETIME(6) NOT NULL,
 UNIQUE KEY uk_route_import_key(user_id,idempotency_key),
 UNIQUE KEY uk_route_import_version(user_id,source_route_id,source_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
-- +goose Down
DROP TABLE learning_route_imports;
DROP TABLE learning_route_bindings;
