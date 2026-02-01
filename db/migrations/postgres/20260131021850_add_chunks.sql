-- migrate:up
CREATE TABLE chunks (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL UNIQUE,
    size INTEGER NOT NULL CHECK (size >= 0),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE nar_file_chunks (
    nar_file_id BIGINT NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    chunk_id BIGINT NOT NULL REFERENCES chunks (id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    PRIMARY KEY (nar_file_id, chunk_index)
);
CREATE INDEX idx_nar_file_chunks_chunk_id ON nar_file_chunks (chunk_id);

-- migrate:down
DROP TABLE IF EXISTS nar_file_chunks;
DROP TABLE IF EXISTS chunks;
