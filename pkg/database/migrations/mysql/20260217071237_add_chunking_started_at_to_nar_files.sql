-- migrate:up
ALTER TABLE nar_files ADD COLUMN chunking_started_at TIMESTAMP NULL;

-- migrate:down
ALTER TABLE nar_files DROP COLUMN chunking_started_at;
