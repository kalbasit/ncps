-- +goose Up
ALTER TABLE nar_files ADD COLUMN total_chunks BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE nar_files DROP COLUMN total_chunks;
