-- migrate:up
CREATE TABLE nars (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id),
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);

-- migrate:down
DROP INDEX idx_nars_id;
DROP INDEX idx_nars_hash;
DROP INDEX idx_nars_narinfo_id;
DROP INDEX idx_nars_last_accessed_at;
DROP TABLE nars;
