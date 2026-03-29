CREATE UNIQUE INDEX IF NOT EXISTS idx_narinfos_id ON narinfos (id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_narinfos_hash ON narinfos (hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nars_id ON nars (id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nars_hash ON nars (hash);
