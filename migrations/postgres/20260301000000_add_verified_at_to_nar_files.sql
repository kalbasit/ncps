-- +goose Up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP WITH TIME ZONE;

-- +goose Down
ALTER TABLE nar_files DROP COLUMN verified_at;
