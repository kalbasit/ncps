-- migrate:up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP NULL;

-- migrate:down
ALTER TABLE nar_files DROP COLUMN verified_at;
