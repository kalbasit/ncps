-- migrate:up
-- Add CASCADE to the foreign key constraint on narinfo_id
-- SQLite doesn't support ALTER TABLE for foreign keys, so we need to
-- recreate the table. This ensures that when a narinfo is deleted,
-- its related nars are automatically deleted

-- Create new table with CASCADE constraint
CREATE TABLE nars_new (
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

-- Copy data from old table
INSERT INTO nars_new (
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
    id,
    narinfo_id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at
FROM nars;

-- Drop old table
DROP TABLE nars;

-- Rename new table
ALTER TABLE nars_new RENAME TO nars;

-- Recreate indexes
CREATE UNIQUE INDEX idx_nars_id ON nars (id);
CREATE UNIQUE INDEX idx_nars_hash ON nars (hash);
CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);

-- migrate:down
-- Revert to foreign key without CASCADE
-- SQLite doesn't support ALTER TABLE for foreign keys, so we need to
-- recreate the table

-- Create table without CASCADE constraint
CREATE TABLE nars_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id),
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Copy data
INSERT INTO nars_old (
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
    id,
    narinfo_id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at
FROM nars;

-- Drop current table
DROP TABLE nars;

-- Rename
ALTER TABLE nars_old RENAME TO nars;

-- Recreate indexes
CREATE UNIQUE INDEX idx_nars_id ON nars (id);
CREATE UNIQUE INDEX idx_nars_hash ON nars (hash);
CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);
