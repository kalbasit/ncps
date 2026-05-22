-- +goose Up
ALTER TABLE nar_files ADD COLUMN chunking_started_at TIMESTAMPTZ NULL;

-- +goose Down
ALTER TABLE nar_files DROP COLUMN chunking_started_at;
