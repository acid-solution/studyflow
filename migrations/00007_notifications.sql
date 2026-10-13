-- +goose Up
CREATE TABLE notification_streams (
    user_id CHAR(36) NOT NULL PRIMARY KEY,
    last_sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
    pruned_through BIGINT UNSIGNED NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE notification_outbox (
    user_id CHAR(36) NOT NULL,
    sequence BIGINT UNSIGNED NOT NULL,
    kind VARCHAR(64) NOT NULL,
    resources JSON NOT NULL,
    topics JSON NOT NULL,
    occurred_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id, sequence),
    KEY idx_notification_expiry (occurred_at, user_id),
    CONSTRAINT fk_notification_stream FOREIGN KEY (user_id) REFERENCES notification_streams(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE notification_outbox;
DROP TABLE notification_streams;
