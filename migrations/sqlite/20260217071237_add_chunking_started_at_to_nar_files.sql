-- +goose Up
ALTER TABLE nar_files ADD COLUMN chunking_started_at TIMESTAMP NULL;

-- +goose Down
ALTER TABLE nar_files DROP COLUMN chunking_started_at;
