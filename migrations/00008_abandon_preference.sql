-- +goose Up
ALTER TABLE user_preferences
    ADD COLUMN default_abandon_parent_effect VARCHAR(16) NOT NULL DEFAULT 'ask';

-- +goose Down
ALTER TABLE user_preferences DROP COLUMN default_abandon_parent_effect;
