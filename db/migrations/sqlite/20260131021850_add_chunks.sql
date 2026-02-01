-- migrate:up
CREATE TABLE chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    size INTEGER NOT NULL CHECK (size >= 0),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP
);

CREATE TABLE nar_file_chunks (
    nar_file_id INTEGER NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    chunk_id INTEGER NOT NULL REFERENCES chunks (id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    PRIMARY KEY (nar_file_id, chunk_index)
);
CREATE INDEX idx_nar_file_chunks_chunk_id ON nar_file_chunks (chunk_id);

-- migrate:down
DROP TABLE IF EXISTS nar_file_chunks;
DROP TABLE IF EXISTS chunks;
