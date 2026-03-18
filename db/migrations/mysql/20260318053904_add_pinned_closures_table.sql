-- migrate:up
CREATE TABLE pinned_closures (
    `id` BIGINT AUTO_INCREMENT PRIMARY KEY,
    `hash` VARCHAR(255) NOT NULL,
    `created_at` TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    `updated_at` TIMESTAMP NULL DEFAULT NULL,
    UNIQUE KEY `idx_pinned_closures_hash` (`hash`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- migrate:down
DROP TABLE IF EXISTS pinned_closures;
