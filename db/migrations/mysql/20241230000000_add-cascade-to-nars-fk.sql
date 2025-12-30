-- migrate:up
-- Add CASCADE to the foreign key constraint on narinfo_id
-- This ensures that when a narinfo is deleted, its related nars are
-- automatically deleted
ALTER TABLE nars DROP FOREIGN KEY fk_nars_narinfo;
ALTER TABLE nars ADD CONSTRAINT fk_nars_narinfo
FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE;

-- migrate:down
-- Revert to foreign key without CASCADE
ALTER TABLE nars DROP FOREIGN KEY fk_nars_narinfo;
ALTER TABLE nars ADD CONSTRAINT fk_nars_narinfo
FOREIGN KEY (narinfo_id) REFERENCES narinfos (id);
