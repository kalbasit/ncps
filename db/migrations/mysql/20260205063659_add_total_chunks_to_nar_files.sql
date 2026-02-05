-- migrate:up
ALTER TABLE nar_files ADD COLUMN total_chunks BIGINT(20) NOT NULL DEFAULT 0;

-- migrate:down
ALTER TABLE nar_files DROP COLUMN total_chunks;
