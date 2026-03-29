CREATE TABLE narinfos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX idx_narinfos_id ON narinfos (id);
CREATE UNIQUE INDEX idx_narinfos_hash ON narinfos (hash);
CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);
