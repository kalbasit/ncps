-- +goose Up
-- create "staging_states" table
CREATE TABLE `staging_states` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `requested_at` datetime NULL, `parts_available` integer NOT NULL DEFAULT (0), `compression` text NOT NULL DEFAULT (''), `status` text NOT NULL DEFAULT ('requested'), CONSTRAINT `staging_states_parts_available_nonneg` CHECK (parts_available >= 0));
-- create index "stagingstate_hash" to table: "staging_states"
CREATE UNIQUE INDEX `stagingstate_hash` ON `staging_states` (`hash`);
-- create index "stagingstate_created_at" to table: "staging_states"
CREATE INDEX `stagingstate_created_at` ON `staging_states` (`created_at`);

-- +goose Down
-- reverse: create index "stagingstate_created_at" to table: "staging_states"
DROP INDEX `stagingstate_created_at`;
-- reverse: create index "stagingstate_hash" to table: "staging_states"
DROP INDEX `stagingstate_hash`;
-- reverse: create "staging_states" table
DROP TABLE `staging_states`;
