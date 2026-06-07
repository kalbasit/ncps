-- +goose Up
-- modify "narinfos" table
ALTER TABLE `narinfos` ADD COLUMN `upstream_url` varchar(255) NULL;

-- +goose Down
-- reverse: modify "narinfos" table
ALTER TABLE `narinfos` DROP COLUMN `upstream_url`;
