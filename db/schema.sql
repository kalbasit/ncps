CREATE TABLE IF NOT EXISTS "schema_migrations" (version varchar(128) primary key);
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
CREATE TABLE nars (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    narinfo_id INTEGER NOT NULL REFERENCES narinfos (id),
    hash TEXT NOT NULL UNIQUE,
    compression TEXT NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP,
    last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
, `query` TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX idx_nars_id ON nars (id);
CREATE UNIQUE INDEX idx_nars_hash ON nars (hash);
CREATE INDEX idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX idx_nars_last_accessed_at ON nars (last_accessed_at);
-- Dbmate schema migrations
INSERT INTO "schema_migrations" (version) VALUES
  ('20241210054814'),
  ('20241210054829'),
  ('20241213014846');
