-- migrate:up
CREATE TABLE chunks (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    hash VARCHAR(64) NOT NULL UNIQUE,
    size INT UNSIGNED NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL
);

CREATE TABLE nar_file_chunks (
    nar_file_id BIGINT NOT NULL,
    chunk_id BIGINT NOT NULL,
    chunk_index BIGINT NOT NULL,
    PRIMARY KEY (nar_file_id, chunk_index),
    FOREIGN KEY (nar_file_id) REFERENCES nar_files (id) ON DELETE CASCADE,
    FOREIGN KEY (chunk_id) REFERENCES chunks (id) ON DELETE CASCADE
);
CREATE INDEX idx_nar_file_chunks_chunk_id ON nar_file_chunks (chunk_id);

-- migrate:down
DROP TABLE IF EXISTS nar_file_chunks;
DROP TABLE IF EXISTS chunks;
