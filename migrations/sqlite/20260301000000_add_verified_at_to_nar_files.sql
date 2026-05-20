-- +goose Up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP;

-- +goose Down
-- Use ALTER TABLE DROP COLUMN (supported in sqlite >= 3.35.0, which the
-- project's mattn/go-sqlite3 v1.14.x bundles) instead of the fragile
-- table-swap pattern. The previous swap-based form had to manually
-- enumerate every column on nar_files and would have silently dropped
-- any column added by a later migration that wasn't yet known here.
ALTER TABLE nar_files DROP COLUMN verified_at;
