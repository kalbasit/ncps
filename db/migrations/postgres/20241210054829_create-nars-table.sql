-- migrate:up
CREATE TABLE nars (
    id BIGSERIAL PRIMARY KEY,
    narinfo_id BIGINT NOT NULL REFERENCES narinfos (id),
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_nars_id ON nars (id);
CREATE UNIQUE INDEX idx_nars_hash ON nars (hash);
CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);

-- migrate:down
DROP INDEX IF EXISTS idx_nars_id;
DROP INDEX IF EXISTS idx_nars_hash;
DROP INDEX IF EXISTS idx_nars_narinfo_id;
DROP INDEX IF EXISTS idx_nars_last_accessed_at;
DROP TABLE IF EXISTS nars;
