-- migrate:up
-- Refactor nars table to support many-to-many relationship with narinfos.
-- This allows multiple narinfos to share the same nar file (same content, different store paths).

-- Step 1: Create new nar_files table without the narinfo_id foreign key
CREATE TABLE nar_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);

-- Step 2: Create join table to establish many-to-many relationship
CREATE TABLE narinfo_nar_files (
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id) ON DELETE CASCADE,
    nar_file_id INTEGER NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    PRIMARY KEY (narinfo_id, nar_file_id)
);

CREATE INDEX idx_narinfo_nar_files_narinfo_id ON narinfo_nar_files (narinfo_id);
CREATE INDEX idx_narinfo_nar_files_nar_file_id ON narinfo_nar_files (nar_file_id);

-- Step 3: Migrate existing data from nars to nar_files
-- Only insert unique nar hashes (in case there are duplicates, though unlikely)
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
SELECT narinfo_id, id
FROM nars;

-- Step 5: Drop old nars table and its indexes
DROP INDEX idx_nars_id;
DROP INDEX idx_nars_hash;
DROP INDEX idx_nars_narinfo_id;
DROP INDEX idx_nars_last_accessed_at;
DROP TABLE nars;

-- migrate:down
-- Recreate the old nars table structure
CREATE TABLE nars (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id) ON DELETE CASCADE,
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);

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
DROP INDEX idx_narinfo_nar_files_narinfo_id;
DROP INDEX idx_narinfo_nar_files_nar_file_id;
DROP TABLE narinfo_nar_files;

DROP INDEX idx_nar_files_id;
DROP INDEX idx_nar_files_hash;
DROP INDEX idx_nar_files_last_accessed_at;
DROP TABLE nar_files;
