-- migrate:up
-- Remove the redundant ref_count column from chunks table
-- SQLite requires recreating the table to drop a column (before version 3.35.0)
CREATE TABLE chunks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    size INTEGER NOT NULL CHECK (size >= 0),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

INSERT INTO chunks_new (id, hash, size, created_at)
SELECT id, hash, size, created_at
FROM chunks;

DROP TABLE chunks;

ALTER TABLE chunks_new RENAME TO chunks;

-- migrate:down
-- Restore the ref_count column (default to 1, will be inaccurate)
CREATE TABLE chunks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    size INTEGER NOT NULL CHECK (size >= 0),
    ref_count INTEGER NOT NULL DEFAULT 1 CHECK (ref_count >= 0),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

INSERT INTO chunks_new (id, hash, size, ref_count, created_at)
SELECT id, hash, size, 1, created_at
FROM chunks;

DROP TABLE chunks;

ALTER TABLE chunks_new RENAME TO chunks;
