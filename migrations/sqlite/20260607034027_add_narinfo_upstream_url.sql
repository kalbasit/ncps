-- +goose Up
-- add column "upstream_url" to table: "narinfos"
ALTER TABLE `narinfos` ADD COLUMN `upstream_url` text NULL;

-- +goose Down
-- reverse: add column "upstream_url" to table: "narinfos"
ALTER TABLE `narinfos` DROP COLUMN `upstream_url`;
