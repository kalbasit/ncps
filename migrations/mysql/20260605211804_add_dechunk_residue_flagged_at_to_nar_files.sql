-- +goose Up
-- modify "nar_files" table
ALTER TABLE `nar_files` ADD COLUMN `dechunk_residue_flagged_at` timestamp NULL;

-- +goose Down
-- reverse: modify "nar_files" table
ALTER TABLE `nar_files` DROP COLUMN `dechunk_residue_flagged_at`;
