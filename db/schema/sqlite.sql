CREATE TABLE IF NOT EXISTS "schema_migrations" (version varchar(128) primary key);
CREATE TABLE narinfos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);
CREATE TABLE IF NOT EXISTS "config" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP
);
CREATE TABLE nar_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_nar_files_last_accessed_at ON nar_files (last_accessed_at);
CREATE TABLE narinfo_nar_files (
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id) ON DELETE CASCADE,
    nar_file_id INTEGER NOT NULL REFERENCES nar_files (id) ON DELETE CASCADE,
    PRIMARY KEY (narinfo_id, nar_file_id)
);
CREATE INDEX idx_narinfo_nar_files_narinfo_id ON narinfo_nar_files (narinfo_id);
CREATE INDEX idx_narinfo_nar_files_nar_file_id ON narinfo_nar_files (nar_file_id);
-- Dbmate schema migrations
INSERT INTO "schema_migrations" (version) VALUES
  ('20241210054814'),
  ('20241210054829'),
  ('20241213014846'),
  ('20251230224159'),
  ('20260101000000'),
  ('20260105025735'),
  ('20260105030513');
