-- +goose Up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP NULL;

-- +goose Down
ALTER TABLE nar_files DROP COLUMN verified_at;
