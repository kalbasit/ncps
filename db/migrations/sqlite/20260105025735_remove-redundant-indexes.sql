-- migrate:up
DROP INDEX IF EXISTS idx_narinfos_id;
DROP INDEX IF EXISTS idx_narinfos_hash;
DROP INDEX IF EXISTS idx_nars_id;
DROP INDEX IF EXISTS idx_nars_hash;

-- migrate:down
CREATE UNIQUE INDEX idx_narinfos_id ON narinfos (id);
CREATE UNIQUE INDEX idx_narinfos_hash ON narinfos (hash);
CREATE UNIQUE INDEX idx_nars_id ON nars (id);
CREATE UNIQUE INDEX idx_nars_hash ON nars (hash);
