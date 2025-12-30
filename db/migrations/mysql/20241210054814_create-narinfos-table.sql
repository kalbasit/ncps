-- migrate:up
CREATE TABLE narinfos (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    hash TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_narinfos_hash (hash(255))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);

-- migrate:down
DROP INDEX IF EXISTS idx_narinfos_last_accessed_at ON narinfos;
DROP TABLE IF EXISTS narinfos;
