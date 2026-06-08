-- +goose Up
-- create "staging_states" table
CREATE TABLE `staging_states` (`id` bigint NOT NULL AUTO_INCREMENT, `created_at` timestamp NULL DEFAULT (current_timestamp()), `updated_at` timestamp NULL, `hash` varchar(255) NOT NULL, `requested_at` timestamp NULL, `parts_available` bigint NOT NULL DEFAULT 0, `compression` varchar(255) NOT NULL DEFAULT '', `status` varchar(255) NOT NULL DEFAULT 'requested', PRIMARY KEY (`id`), INDEX `stagingstate_created_at` (`created_at`), UNIQUE INDEX `stagingstate_hash` (`hash`), CONSTRAINT `staging_states_parts_available_nonneg` CHECK (`parts_available` >= 0)) CHARSET utf8mb4 COLLATE utf8mb4_bin;

-- +goose Down
-- reverse: create "staging_states" table
DROP TABLE `staging_states`;
