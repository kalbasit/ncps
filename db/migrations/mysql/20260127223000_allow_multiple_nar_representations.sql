-- migrate:up
ALTER TABLE nar_files MODIFY query VARCHAR(512) NOT NULL DEFAULT '' AFTER last_accessed_at;
ALTER TABLE nar_files DROP INDEX idx_nar_files_hash;
ALTER TABLE nar_files ADD UNIQUE KEY idx_nar_files_hash_compression_query (hash, compression, query);

-- migrate:down
ALTER TABLE nar_files DROP INDEX idx_nar_files_hash_compression_query;
ALTER TABLE nar_files ADD UNIQUE KEY idx_nar_files_hash (hash);
ALTER TABLE nar_files MODIFY query TEXT NOT NULL;
