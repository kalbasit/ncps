-- migrate:up
-- Remove the redundant ref_count column from chunks table
ALTER TABLE chunks DROP COLUMN ref_count;

-- migrate:down
-- Restore the ref_count column (default to 0, will be inaccurate)
ALTER TABLE chunks ADD COLUMN ref_count INTEGER NOT NULL DEFAULT 0;
