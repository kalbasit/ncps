-- migrate:up
-- Narinfos Table
CREATE TABLE narinfos (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_narinfos_hash (hash),
    KEY idx_narinfos_last_accessed_at (last_accessed_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Nar Files Table (Stores the physical file info)
CREATE TABLE nar_files (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    hash VARCHAR(255) NOT NULL,
    compression VARCHAR(50) NOT NULL,
    file_size BIGINT UNSIGNED NOT NULL,
    query TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_nar_files_hash (hash),
    KEY idx_nar_files_last_accessed_at (last_accessed_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Join Table (Many-to-Many)
CREATE TABLE narinfo_nar_files (
    narinfo_id BIGINT NOT NULL,
    nar_file_id BIGINT NOT NULL,
    PRIMARY KEY (narinfo_id, nar_file_id),
    KEY idx_narinfo_nar_files_nar_file_id (nar_file_id),
    CONSTRAINT fk_narinfo_nar_files_narinfo FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE,
    CONSTRAINT fk_narinfo_nar_files_nar_file FOREIGN KEY (nar_file_id) REFERENCES nar_files (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- migrate:down
DROP TABLE IF EXISTS narinfo_nar_files;
DROP TABLE IF EXISTS nar_files;
DROP TABLE IF EXISTS narinfos;
