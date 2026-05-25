-- +goose Up
-- create "build_trace_entries" table
CREATE TABLE `build_trace_entries` (`id` bigint NOT NULL AUTO_INCREMENT, `created_at` timestamp NULL DEFAULT (current_timestamp()), `updated_at` timestamp NULL, `drv_path` varchar(255) NOT NULL, `output_name` varchar(255) NOT NULL, `out_path` varchar(255) NOT NULL, `raw_json` longtext NOT NULL, PRIMARY KEY (`id`), UNIQUE INDEX `buildtraceentry_drv_path_output_name` (`drv_path`, `output_name`)) CHARSET utf8mb4 COLLATE utf8mb4_bin;
-- create "build_trace_signatures" table
CREATE TABLE `build_trace_signatures` (`id` bigint NOT NULL AUTO_INCREMENT, `key_name` varchar(255) NOT NULL, `signature` varchar(255) NOT NULL, `build_trace_entry_id` bigint NOT NULL, PRIMARY KEY (`id`), INDEX `buildtracesignature_build_trace_entry_id` (`build_trace_entry_id`), UNIQUE INDEX `buildtracesignature_build_trace_entry_id_key_name` (`build_trace_entry_id`, `key_name`), CONSTRAINT `build_trace_signatures_build_trace_entries_signatures` FOREIGN KEY (`build_trace_entry_id`) REFERENCES `build_trace_entries` (`id`) ON UPDATE RESTRICT ON DELETE CASCADE) CHARSET utf8mb4 COLLATE utf8mb4_bin;

-- +goose Down
-- reverse: create "build_trace_signatures" table
DROP TABLE `build_trace_signatures`;
-- reverse: create "build_trace_entries" table
DROP TABLE `build_trace_entries`;
