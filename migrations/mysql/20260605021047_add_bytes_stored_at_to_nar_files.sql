-- +goose Up
-- modify "nar_files" table
ALTER TABLE `nar_files` ADD COLUMN `bytes_stored_at` timestamp NULL;

-- +goose Down
-- reverse: modify "nar_files" table
ALTER TABLE `nar_files` DROP COLUMN `bytes_stored_at`;
