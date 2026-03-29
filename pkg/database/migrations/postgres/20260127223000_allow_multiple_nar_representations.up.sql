ALTER TABLE nar_files DROP CONSTRAINT nar_files_hash_key;
ALTER TABLE nar_files ADD CONSTRAINT nar_files_hash_compression_query_key UNIQUE (hash, compression, query);
