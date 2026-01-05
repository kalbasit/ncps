-- migrate:up
-- Refactor nars table to support many-to-many relationship with narinfos.
-- This allows multiple narinfos to share the same nar file (same content, different store paths).

-- Step 1: Create new nar_files table without the narinfo_id foreign key
CREATE TABLE nar_files (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL,
    file_size BIGINT UNSIGNED NOT NULL,
    query TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_nar_files_hash (hash (255)),
    KEY idx_nar_files_last_accessed_at (last_accessed_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Step 2: Create join table to establish many-to-many relationship
CREATE TABLE narinfo_nar_files (
    narinfo_id BIGINT NOT NULL,
    nar_file_id BIGINT NOT NULL,
    PRIMARY KEY (narinfo_id, nar_file_id),
    KEY idx_narinfo_nar_files_narinfo_id (narinfo_id),
    KEY idx_narinfo_nar_files_nar_file_id (nar_file_id),
    CONSTRAINT fk_narinfo_nar_files_narinfo FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE,
    CONSTRAINT fk_narinfo_nar_files_nar_file FOREIGN KEY (nar_file_id) REFERENCES nar_files (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Step 3: Migrate existing data from nars to nar_files
INSERT INTO nar_files (
    id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at
)
SELECT
    id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at
FROM nars;

-- Step 4: Populate join table with existing relationships
INSERT INTO narinfo_nar_files (narinfo_id, nar_file_id)
SELECT
    narinfo_id,
    id
FROM nars;

-- Step 5: Drop old nars table
DROP TABLE nars;

-- migrate:down
-- Recreate the old nars table structure
CREATE TABLE nars (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    narinfo_id BIGINT NOT NULL,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL,
    file_size BIGINT UNSIGNED NOT NULL,
    query TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY idx_nars_hash (hash (255)),
    KEY idx_nars_narinfo_id (narinfo_id),
    KEY idx_nars_last_accessed_at (last_accessed_at),
    CONSTRAINT fk_nars_narinfo FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Migrate data back from nar_files to nars
-- Note: This assumes one-to-one relationship and will fail if there are shared nar_files
INSERT INTO nars (
    id,
    narinfo_id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at
)
SELECT
    nf.id,
    nnf.narinfo_id,
    nf.hash,
    nf.compression,
    nf.file_size,
    nf.query,
    nf.created_at,
    nf.updated_at,
    nf.last_accessed_at
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id;

-- Drop new tables
DROP TABLE narinfo_nar_files;
DROP TABLE nar_files;
