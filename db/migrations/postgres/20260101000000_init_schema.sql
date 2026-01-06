-- migrate:up
-- Narinfos Table
CREATE TABLE narinfos (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ,
    last_accessed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);

-- Nar Files Table
CREATE TABLE nar_files (
    id BIGSERIAL PRIMARY KEY,
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL CHECK (file_size >= 0),
    query TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMPTZ,
    last_accessed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);

-- Join Table
CREATE TABLE narinfo_nar_files (
    narinfo_id BIGINT NOT NULL REFERENCES narinfos (id) ON DELETE CASCADE,
    nar_file_id BIGINT NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    PRIMARY KEY (narinfo_id, nar_file_id)
);
CREATE INDEX idx_narinfo_nar_files_nar_file_id ON narinfo_nar_files (nar_file_id);

-- migrate:down
DROP TABLE IF EXISTS narinfo_nar_files;
DROP TABLE IF EXISTS nar_files;
DROP TABLE IF EXISTS narinfos;
