-- +goose Up
-- Where a plan or task came from. 'user' means a person created it through the
-- app; 'agent' means it arrived through the MCP import. Existing rows are all
-- user-created, which is what the default records.
--
-- A plan carries the flag as well as its tasks because an imported plan can be
-- extended by hand afterwards: the plan stays agent-originated while the tasks
-- the user adds to it do not.
ALTER TABLE plans ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT 'user';
ALTER TABLE tasks ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT 'user';

-- +goose Down
ALTER TABLE tasks DROP COLUMN source;
ALTER TABLE plans DROP COLUMN source;
