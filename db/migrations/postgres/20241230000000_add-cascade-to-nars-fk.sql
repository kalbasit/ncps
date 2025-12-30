-- migrate:up
-- Add CASCADE to the foreign key constraint on narinfo_id
-- This ensures that when a narinfo is deleted, its related nars are
-- automatically deleted
ALTER TABLE nars DROP CONSTRAINT IF EXISTS nars_narinfo_id_fkey;
ALTER TABLE nars ADD CONSTRAINT nars_narinfo_id_fkey
FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE;

-- migrate:down
-- Revert to foreign key without CASCADE
ALTER TABLE nars DROP CONSTRAINT IF EXISTS nars_narinfo_id_fkey;
ALTER TABLE nars ADD CONSTRAINT nars_narinfo_id_fkey
FOREIGN KEY (narinfo_id) REFERENCES narinfos (id);
