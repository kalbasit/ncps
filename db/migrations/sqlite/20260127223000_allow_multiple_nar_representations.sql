-- migrate:up

-- Step 1: Create new nar_files table with the composite unique constraint
CREATE TABLE nar_files_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    "query" TEXT NOT NULL DEFAULT '',
    UNIQUE (hash, compression, "query")
);

-- Step 2: Copy data from old table to new table
INSERT INTO nar_files_new (
    id, hash, compression, file_size, "query", created_at, updated_at, last_accessed_at
)
SELECT
    id,
    hash,
    compression,
    file_size,
    "query",
    created_at,
    updated_at,
    last_accessed_at
FROM nar_files;

-- Step 3: Disable foreign keys temporarily to swap tables
PRAGMA foreign_keys = OFF;

-- Step 4: Drop old table and rename new table
DROP TABLE nar_files;
ALTER TABLE nar_files_new RENAME TO nar_files;

-- Step 5: Re-enable foreign keys
PRAGMA foreign_keys = ON;

-- Step 6: Re-create index
CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);

-- migrate:down
CREATE TABLE nar_files_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    "query" TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Note: This will fail if there are non-unique hashes across different compressions/queries.
INSERT INTO nar_files_old (
    id, hash, compression, file_size, "query", created_at, updated_at, last_accessed_at
)
SELECT
    id,
    hash,
    compression,
    file_size,
    "query",
    created_at,
    updated_at,
    last_accessed_at
FROM nar_files;

PRAGMA foreign_keys = OFF;
DROP TABLE nar_files;
ALTER TABLE nar_files_old RENAME TO nar_files;
PRAGMA foreign_keys = ON;

CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);
