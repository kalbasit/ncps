-- +goose Up
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_narinfos" table
CREATE TABLE `new_narinfos` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `store_path` text NULL, `url` text NULL, `compression` text NULL, `file_hash` text NULL, `file_size` integer NULL, `nar_hash` text NULL, `nar_size` integer NULL, `deriver` text NULL, `system` text NULL, `ca` text NULL, `last_accessed_at` datetime NULL, CONSTRAINT `narinfos_file_size_nonneg` CHECK (file_size >= 0), CONSTRAINT `narinfos_nar_size_nonneg` CHECK (nar_size >= 0));
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
-- create "new_config" table
CREATE TABLE `new_config` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `key` text NOT NULL, `value` text NOT NULL);
-- copy rows from old table "config" to new temporary table "new_config"
INSERT INTO `new_config` (`id`, `created_at`, `updated_at`, `key`, `value`) SELECT `id`, `created_at`, `updated_at`, `key`, `value` FROM `config`;
-- drop "config" table after copying rows
DROP TABLE `config`;
-- rename temporary table "new_config" to "config"
ALTER TABLE `new_config` RENAME TO `config`;
-- create index "configentry_key" to table: "config"
CREATE UNIQUE INDEX `configentry_key` ON `config` (`key`);
-- create "new_narinfo_nar_files" table
CREATE TABLE `new_narinfo_nar_files` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `nar_file_id` integer NOT NULL, `narinfo_id` integer NOT NULL, CONSTRAINT `narinfo_nar_files_nar_files_nar_info_nar_files` FOREIGN KEY (`nar_file_id`) REFERENCES `nar_files` (`id`) ON DELETE CASCADE, CONSTRAINT `narinfo_nar_files_narinfos_nar_info_nar_files` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE);
-- copy rows from old table "narinfo_nar_files" to new temporary table "new_narinfo_nar_files"
INSERT INTO `new_narinfo_nar_files` (`nar_file_id`, `narinfo_id`) SELECT `nar_file_id`, `narinfo_id` FROM `narinfo_nar_files`;
-- drop "narinfo_nar_files" table after copying rows
DROP TABLE `narinfo_nar_files`;
-- rename temporary table "new_narinfo_nar_files" to "narinfo_nar_files"
ALTER TABLE `new_narinfo_nar_files` RENAME TO `narinfo_nar_files`;
-- create index "narinfonarfile_narinfo_id_nar_file_id" to table: "narinfo_nar_files"
CREATE UNIQUE INDEX `narinfonarfile_narinfo_id_nar_file_id` ON `narinfo_nar_files` (`narinfo_id`, `nar_file_id`);
-- create index "narinfonarfile_narinfo_id" to table: "narinfo_nar_files"
CREATE INDEX `narinfonarfile_narinfo_id` ON `narinfo_nar_files` (`narinfo_id`);
-- create index "narinfonarfile_nar_file_id" to table: "narinfo_nar_files"
CREATE INDEX `narinfonarfile_nar_file_id` ON `narinfo_nar_files` (`nar_file_id`);
-- create "new_narinfo_references" table
CREATE TABLE `new_narinfo_references` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `reference` text NOT NULL, `narinfo_id` integer NOT NULL, CONSTRAINT `narinfo_references_narinfos_references` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE);
-- copy rows from old table "narinfo_references" to new temporary table "new_narinfo_references"
INSERT INTO `new_narinfo_references` (`reference`, `narinfo_id`) SELECT `reference`, `narinfo_id` FROM `narinfo_references`;
-- drop "narinfo_references" table after copying rows
DROP TABLE `narinfo_references`;
-- rename temporary table "new_narinfo_references" to "narinfo_references"
ALTER TABLE `new_narinfo_references` RENAME TO `narinfo_references`;
-- create index "narinforeference_narinfo_id_reference" to table: "narinfo_references"
CREATE UNIQUE INDEX `narinforeference_narinfo_id_reference` ON `narinfo_references` (`narinfo_id`, `reference`);
-- create index "narinforeference_reference" to table: "narinfo_references"
CREATE INDEX `narinforeference_reference` ON `narinfo_references` (`reference`);
-- create "new_narinfo_signatures" table
CREATE TABLE `new_narinfo_signatures` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `signature` text NOT NULL, `narinfo_id` integer NOT NULL, CONSTRAINT `narinfo_signatures_narinfos_signatures` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE);
-- copy rows from old table "narinfo_signatures" to new temporary table "new_narinfo_signatures"
INSERT INTO `new_narinfo_signatures` (`signature`, `narinfo_id`) SELECT `signature`, `narinfo_id` FROM `narinfo_signatures`;
-- drop "narinfo_signatures" table after copying rows
DROP TABLE `narinfo_signatures`;
-- rename temporary table "new_narinfo_signatures" to "narinfo_signatures"
ALTER TABLE `new_narinfo_signatures` RENAME TO `narinfo_signatures`;
-- create index "narinfosignature_narinfo_id_signature" to table: "narinfo_signatures"
CREATE UNIQUE INDEX `narinfosignature_narinfo_id_signature` ON `narinfo_signatures` (`narinfo_id`, `signature`);
-- create index "narinfosignature_signature" to table: "narinfo_signatures"
CREATE INDEX `narinfosignature_signature` ON `narinfo_signatures` (`signature`);
-- create "new_nar_files" table
CREATE TABLE `new_nar_files` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `compression` text NOT NULL DEFAULT (''), `file_size` integer NOT NULL, `query` text NOT NULL DEFAULT (''), `total_chunks` integer NOT NULL DEFAULT (0), `chunking_started_at` datetime NULL, `verified_at` datetime NULL, `last_accessed_at` datetime NULL);
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
-- create "new_chunks" table
CREATE TABLE `new_chunks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL, `size` integer NOT NULL, `compressed_size` integer NOT NULL DEFAULT (0), CONSTRAINT `chunks_compressed_size_nonneg` CHECK (compressed_size >= 0), CONSTRAINT `chunks_size_nonneg` CHECK (size >= 0));
-- copy rows from old table "chunks" to new temporary table "new_chunks"
INSERT INTO `new_chunks` (`id`, `created_at`, `updated_at`, `hash`, `size`, `compressed_size`) SELECT `id`, `created_at`, `updated_at`, `hash`, `size`, `compressed_size` FROM `chunks`;
-- drop "chunks" table after copying rows
DROP TABLE `chunks`;
-- rename temporary table "new_chunks" to "chunks"
ALTER TABLE `new_chunks` RENAME TO `chunks`;
-- create index "chunk_hash" to table: "chunks"
CREATE UNIQUE INDEX `chunk_hash` ON `chunks` (`hash`);
-- create "new_nar_file_chunks" table
CREATE TABLE `new_nar_file_chunks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `chunk_index` integer NOT NULL, `chunk_id` integer NOT NULL, `nar_file_id` integer NOT NULL, CONSTRAINT `nar_file_chunks_chunks_nar_file_links` FOREIGN KEY (`chunk_id`) REFERENCES `chunks` (`id`) ON DELETE CASCADE, CONSTRAINT `nar_file_chunks_nar_files_chunk_links` FOREIGN KEY (`nar_file_id`) REFERENCES `nar_files` (`id`) ON DELETE CASCADE);
-- copy rows from old table "nar_file_chunks" to new temporary table "new_nar_file_chunks"
INSERT INTO `new_nar_file_chunks` (`chunk_index`, `chunk_id`, `nar_file_id`) SELECT `chunk_index`, `chunk_id`, `nar_file_id` FROM `nar_file_chunks`;
-- drop "nar_file_chunks" table after copying rows
DROP TABLE `nar_file_chunks`;
-- rename temporary table "new_nar_file_chunks" to "nar_file_chunks"
ALTER TABLE `new_nar_file_chunks` RENAME TO `nar_file_chunks`;
-- create index "narfilechunk_nar_file_id_chunk_index" to table: "nar_file_chunks"
CREATE UNIQUE INDEX `narfilechunk_nar_file_id_chunk_index` ON `nar_file_chunks` (`nar_file_id`, `chunk_index`);
-- create index "narfilechunk_chunk_id" to table: "nar_file_chunks"
CREATE INDEX `narfilechunk_chunk_id` ON `nar_file_chunks` (`chunk_id`);
-- create "new_pinned_closures" table
CREATE TABLE `new_pinned_closures` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` datetime NULL, `hash` text NOT NULL);
-- copy rows from old table "pinned_closures" to new temporary table "new_pinned_closures"
INSERT INTO `new_pinned_closures` (`id`, `created_at`, `updated_at`, `hash`) SELECT `id`, `created_at`, `updated_at`, `hash` FROM `pinned_closures`;
-- drop "pinned_closures" table after copying rows
DROP TABLE `pinned_closures`;
-- rename temporary table "new_pinned_closures" to "pinned_closures"
ALTER TABLE `new_pinned_closures` RENAME TO `pinned_closures`;
-- create index "pinnedclosure_hash" to table: "pinned_closures"
CREATE UNIQUE INDEX `pinnedclosure_hash` ON `pinned_closures` (`hash`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- +goose Down
-- reverse: create index "pinnedclosure_hash" to table: "pinned_closures"
DROP INDEX `pinnedclosure_hash`;
-- reverse: create "new_pinned_closures" table
DROP TABLE `new_pinned_closures`;
-- reverse: create index "narfilechunk_chunk_id" to table: "nar_file_chunks"
DROP INDEX `narfilechunk_chunk_id`;
-- reverse: create index "narfilechunk_nar_file_id_chunk_index" to table: "nar_file_chunks"
DROP INDEX `narfilechunk_nar_file_id_chunk_index`;
-- reverse: create "new_nar_file_chunks" table
DROP TABLE `new_nar_file_chunks`;
-- reverse: create index "chunk_hash" to table: "chunks"
DROP INDEX `chunk_hash`;
-- reverse: create "new_chunks" table
DROP TABLE `new_chunks`;
-- reverse: create index "narfile_last_accessed_at" to table: "nar_files"
DROP INDEX `narfile_last_accessed_at`;
-- reverse: create index "narfile_hash_compression_query" to table: "nar_files"
DROP INDEX `narfile_hash_compression_query`;
-- reverse: create "new_nar_files" table
DROP TABLE `new_nar_files`;
-- reverse: create index "narinfosignature_signature" to table: "narinfo_signatures"
DROP INDEX `narinfosignature_signature`;
-- reverse: create index "narinfosignature_narinfo_id_signature" to table: "narinfo_signatures"
DROP INDEX `narinfosignature_narinfo_id_signature`;
-- reverse: create "new_narinfo_signatures" table
DROP TABLE `new_narinfo_signatures`;
-- reverse: create index "narinforeference_reference" to table: "narinfo_references"
DROP INDEX `narinforeference_reference`;
-- reverse: create index "narinforeference_narinfo_id_reference" to table: "narinfo_references"
DROP INDEX `narinforeference_narinfo_id_reference`;
-- reverse: create "new_narinfo_references" table
DROP TABLE `new_narinfo_references`;
-- reverse: create index "narinfonarfile_nar_file_id" to table: "narinfo_nar_files"
DROP INDEX `narinfonarfile_nar_file_id`;
-- reverse: create index "narinfonarfile_narinfo_id" to table: "narinfo_nar_files"
DROP INDEX `narinfonarfile_narinfo_id`;
-- reverse: create index "narinfonarfile_narinfo_id_nar_file_id" to table: "narinfo_nar_files"
DROP INDEX `narinfonarfile_narinfo_id_nar_file_id`;
-- reverse: create "new_narinfo_nar_files" table
DROP TABLE `new_narinfo_nar_files`;
-- reverse: create index "configentry_key" to table: "config"
DROP INDEX `configentry_key`;
-- reverse: create "new_config" table
DROP TABLE `new_config`;
-- reverse: create index "narinfo_last_accessed_at" to table: "narinfos"
DROP INDEX `narinfo_last_accessed_at`;
-- reverse: create index "narinfo_hash" to table: "narinfos"
DROP INDEX `narinfo_hash`;
-- reverse: create "new_narinfos" table
DROP TABLE `new_narinfos`;
