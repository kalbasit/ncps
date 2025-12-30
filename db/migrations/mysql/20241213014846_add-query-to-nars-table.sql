-- migrate:up
ALTER TABLE nars ADD COLUMN query TEXT NOT NULL DEFAULT '';

-- migrate:down
ALTER TABLE nars DROP COLUMN query;
