-- +goose Up
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_narinfos" table
CREATE TABLE `new_narinfos` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `store_path` text NULL, `url` text NULL, `compression` text NULL, `file_hash` text NULL, `file_size` integer NULL, `nar_hash` text NULL, `nar_size` integer NULL, `deriver` text NULL, `system` text NULL, `ca` text NULL, `last_accessed_at` datetime NULL DEFAULT (CURRENT_TIMESTAMP), CONSTRAINT `narinfos_file_size_nonneg` CHECK (file_size >= 0), CONSTRAINT `narinfos_nar_size_nonneg` CHECK (nar_size >= 0));
-- copy rows from old table "narinfos" to new temporary table "new_narinfos"
INSERT INTO `new_narinfos` (`id`, `created_at`, `updated_at`, `hash`, `store_path`, `url`, `compression`, `file_hash`, `file_size`, `nar_hash`, `nar_size`, `deriver`, `system`, `ca`, `last_accessed_at`) SELECT `id`, `created_at`, `updated_at`, `hash`, `store_path`, `url`, `compression`, `file_hash`, `file_size`, `nar_hash`, `nar_size`, `deriver`, `system`, `ca`, `last_accessed_at` FROM `narinfos`;
-- drop "narinfos" table after copying rows
DROP TABLE `narinfos`;
-- rename temporary table "new_narinfos" to "narinfos"
ALTER TABLE `new_narinfos` RENAME TO `narinfos`;
-- create index "narinfo_hash" to table: "narinfos"
CREATE UNIQUE INDEX `narinfo_hash` ON `narinfos` (`hash`);
-- create index "narinfo_last_accessed_at" to table: "narinfos"
CREATE INDEX `narinfo_last_accessed_at` ON `narinfos` (`last_accessed_at`);
-- create "new_nar_files" table
CREATE TABLE `new_nar_files` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `compression` text NOT NULL DEFAULT (''), `file_size` integer NOT NULL, `query` text NOT NULL DEFAULT (''), `total_chunks` integer NOT NULL DEFAULT (0), `chunking_started_at` datetime NULL, `verified_at` datetime NULL, `last_accessed_at` datetime NULL DEFAULT (CURRENT_TIMESTAMP));
-- copy rows from old table "nar_files" to new temporary table "new_nar_files"
INSERT INTO `new_nar_files` (`id`, `created_at`, `updated_at`, `hash`, `compression`, `file_size`, `query`, `total_chunks`, `chunking_started_at`, `verified_at`, `last_accessed_at`) SELECT `id`, `created_at`, `updated_at`, `hash`, `compression`, `file_size`, `query`, `total_chunks`, `chunking_started_at`, `verified_at`, `last_accessed_at` FROM `nar_files`;
-- drop "nar_files" table after copying rows
DROP TABLE `nar_files`;
-- rename temporary table "new_nar_files" to "nar_files"
ALTER TABLE `new_nar_files` RENAME TO `nar_files`;
-- create index "narfile_hash_compression_query" to table: "nar_files"
CREATE UNIQUE INDEX `narfile_hash_compression_query` ON `nar_files` (`hash`, `compression`, `query`);
-- create index "narfile_last_accessed_at" to table: "nar_files"
CREATE INDEX `narfile_last_accessed_at` ON `nar_files` (`last_accessed_at`);
-- create "build_trace_entries" table
CREATE TABLE `build_trace_entries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `drv_path` text NOT NULL, `output_name` text NOT NULL, `out_path` text NOT NULL, `raw_json` text NOT NULL);
-- create index "buildtraceentry_drv_path_output_name" to table: "build_trace_entries"
CREATE UNIQUE INDEX `buildtraceentry_drv_path_output_name` ON `build_trace_entries` (`drv_path`, `output_name`);
-- create "build_trace_signatures" table
CREATE TABLE `build_trace_signatures` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `key_name` text NOT NULL, `signature` text NOT NULL, `build_trace_entry_id` integer NOT NULL, CONSTRAINT `build_trace_signatures_build_trace_entries_signatures` FOREIGN KEY (`build_trace_entry_id`) REFERENCES `build_trace_entries` (`id`) ON DELETE CASCADE);
-- create index "buildtracesignature_build_trace_entry_id_key_name" to table: "build_trace_signatures"
CREATE UNIQUE INDEX `buildtracesignature_build_trace_entry_id_key_name` ON `build_trace_signatures` (`build_trace_entry_id`, `key_name`);
-- create index "buildtracesignature_build_trace_entry_id" to table: "build_trace_signatures"
CREATE INDEX `buildtracesignature_build_trace_entry_id` ON `build_trace_signatures` (`build_trace_entry_id`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- +goose Down
-- reverse: create index "buildtracesignature_build_trace_entry_id" to table: "build_trace_signatures"
DROP INDEX `buildtracesignature_build_trace_entry_id`;
-- reverse: create index "buildtracesignature_build_trace_entry_id_key_name" to table: "build_trace_signatures"
DROP INDEX `buildtracesignature_build_trace_entry_id_key_name`;
-- reverse: create "build_trace_signatures" table
DROP TABLE `build_trace_signatures`;
-- reverse: create index "buildtraceentry_drv_path_output_name" to table: "build_trace_entries"
DROP INDEX `buildtraceentry_drv_path_output_name`;
-- reverse: create "build_trace_entries" table
DROP TABLE `build_trace_entries`;
-- reverse: create index "narfile_last_accessed_at" to table: "nar_files"
DROP INDEX `narfile_last_accessed_at`;
-- reverse: create index "narfile_hash_compression_query" to table: "nar_files"
DROP INDEX `narfile_hash_compression_query`;
-- reverse: create "new_nar_files" table
DROP TABLE `new_nar_files`;
-- reverse: create index "narinfo_last_accessed_at" to table: "narinfos"
DROP INDEX `narinfo_last_accessed_at`;
-- reverse: create index "narinfo_hash" to table: "narinfos"
DROP INDEX `narinfo_hash`;
-- reverse: create "new_narinfos" table
DROP TABLE `new_narinfos`;
