-- migrate:up
CREATE TABLE nars (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    narinfo_id BIGINT NOT NULL,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL,
    file_size BIGINT UNSIGNED NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_nars_hash (hash (255)),
    KEY idx_nars_narinfo_id (narinfo_id),
    KEY idx_nars_last_accessed_at (last_accessed_at),
    CONSTRAINT fk_nars_narinfo FOREIGN KEY (narinfo_id) REFERENCES narinfos (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- migrate:down
DROP TABLE IF EXISTS nars;
