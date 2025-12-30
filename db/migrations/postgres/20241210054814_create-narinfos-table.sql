-- migrate:up
CREATE TABLE narinfos (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);

-- migrate:down
DROP INDEX IF EXISTS idx_narinfos_last_accessed_at;
DROP TABLE IF EXISTS narinfos;
