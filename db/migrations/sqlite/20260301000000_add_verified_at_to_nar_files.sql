-- migrate:up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP;

-- migrate:down
CREATE TABLE nar_files_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    "query" TEXT NOT NULL DEFAULT '',
    total_chunks BIGINT NOT NULL DEFAULT 0,
    chunking_started_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (hash, compression, "query")
);

INSERT INTO nar_files_new (
    id, hash, compression, file_size, "query", total_chunks, chunking_started_at, created_at, updated_at, last_accessed_at
)
SELECT
    id, hash, compression, file_size, "query", total_chunks, chunking_started_at, created_at, updated_at, last_accessed_at
FROM nar_files;

PRAGMA foreign_keys = OFF;
DROP TABLE nar_files;
ALTER TABLE nar_files_new RENAME TO nar_files;
PRAGMA foreign_keys = ON;

CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);
