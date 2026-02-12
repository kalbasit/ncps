CREATE TABLE IF NOT EXISTS "schema_migrations" (version varchar(128) primary key);
CREATE TABLE narinfos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
, store_path TEXT, url TEXT, compression TEXT, file_hash TEXT, file_size BIGINT CHECK (file_size >= 0), nar_hash TEXT, nar_size BIGINT CHECK (nar_size >= 0), deriver TEXT, system TEXT, ca TEXT);
CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);
CREATE TABLE IF NOT EXISTS "config" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP
);
CREATE TABLE narinfo_nar_files (
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id) ON DELETE CASCADE,
    nar_file_id INTEGER NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    PRIMARY KEY (narinfo_id, nar_file_id)
);
CREATE INDEX idx_narinfo_nar_files_narinfo_id ON narinfo_nar_files (narinfo_id);
CREATE INDEX idx_narinfo_nar_files_nar_file_id ON narinfo_nar_files (nar_file_id);
CREATE TABLE narinfo_references (
    narinfo_id BIGINT NOT NULL,
    reference TEXT NOT NULL,
    PRIMARY KEY (narinfo_id, reference),
    FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
);
CREATE INDEX idx_narinfo_references_reference ON narinfo_references (reference);
CREATE TABLE narinfo_signatures (
    narinfo_id BIGINT NOT NULL,
    signature TEXT NOT NULL,
    PRIMARY KEY (narinfo_id, signature),
    FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
);
CREATE INDEX idx_narinfo_signatures_signature ON narinfo_signatures (signature);
CREATE TABLE IF NOT EXISTS "nar_files" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    "query" TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, total_chunks BIGINT NOT NULL DEFAULT 0,
    UNIQUE (hash, compression, "query")
);
CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);
CREATE TABLE chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    size INTEGER NOT NULL CHECK (size >= 0),
    compressed_size INTEGER NOT NULL DEFAULT 0 CHECK (compressed_size >= 0),
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
-- Dbmate schema migrations
INSERT INTO "schema_migrations" (version) VALUES
  ('20241210054814'),
  ('20241210054829'),
  ('20241213014846'),
  ('20251230224159'),
  ('20260101000000'),
  ('20260105025735'),
  ('20260105030513'),
  ('20260117195000'),
  ('20260127223000'),
  ('20260131021850'),
  ('20260205063651');
