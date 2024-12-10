CREATE TABLE IF NOT EXISTS narinfos (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  hash TEXT NOT NULL UNIQUE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
  updated_at TIMESTAMP,
  last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_narinfos_id ON narinfos (id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_narinfos_hash ON narinfos (hash);
CREATE INDEX IF NOT EXISTS idx_narinfos_last_accessed_at ON narinfos (last_accessed_at);

CREATE TABLE IF NOT EXISTS nars (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  narinfo_id INTEGER NOT NULL REFERENCES narinfos(id),
  hash TEXT NOT NULL UNIQUE,
  compression TEXT NOT NULL DEFAULT '',
  file_size INTEGER NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
  updated_at TIMESTAMP,
  last_accessed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_nars_id ON nars (id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nars_hash ON nars (hash);
CREATE INDEX IF NOT EXISTS idx_nars_narinfo_id ON nars (narinfo_id);
CREATE INDEX IF NOT EXISTS idx_nars_last_accessed_at ON nars (last_accessed_at);
