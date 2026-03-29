-- migrate:up
ALTER TABLE nar_files ADD COLUMN verified_at TIMESTAMP WITH TIME ZONE;

-- migrate:down
ALTER TABLE nar_files DROP COLUMN verified_at;
