-- +goose Up
ALTER TABLE nar_files ADD COLUMN bytes_stored_at TIMESTAMP;

-- +goose Down
-- ALTER TABLE DROP COLUMN is supported in sqlite >= 3.35.0 (bundled by the
-- project's mattn/go-sqlite3 v1.14.x), so drop the column directly rather than
-- the fragile table-swap pattern.
ALTER TABLE nar_files DROP COLUMN bytes_stored_at;
