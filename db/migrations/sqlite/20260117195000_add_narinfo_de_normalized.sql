-- migrate:up
-- Migration to add de-normalized NarInfo fields and helper tables

-- Add columns to narinfos table
ALTER TABLE narinfos ADD COLUMN store_path TEXT;
ALTER TABLE narinfos ADD COLUMN url TEXT;
ALTER TABLE narinfos ADD COLUMN compression TEXT;
ALTER TABLE narinfos ADD COLUMN file_hash TEXT;
ALTER TABLE narinfos ADD COLUMN file_size BIGINT CHECK (file_size >= 0);
ALTER TABLE narinfos ADD COLUMN nar_hash TEXT;
ALTER TABLE narinfos ADD COLUMN nar_size BIGINT CHECK (nar_size >= 0);
ALTER TABLE narinfos ADD COLUMN deriver TEXT;
ALTER TABLE narinfos ADD COLUMN system TEXT;
ALTER TABLE narinfos ADD COLUMN ca TEXT;

-- Create references table
CREATE TABLE narinfo_references (
    narinfo_id BIGINT NOT NULL,
    reference TEXT NOT NULL,
    PRIMARY KEY (narinfo_id, reference),
    FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
);
CREATE INDEX idx_narinfo_references_reference ON narinfo_references (reference);

-- Create signatures table
CREATE TABLE narinfo_signatures (
    narinfo_id BIGINT NOT NULL,
    signature TEXT NOT NULL,
    PRIMARY KEY (narinfo_id, signature),
    FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
);
CREATE INDEX idx_narinfo_signatures_signature ON narinfo_signatures (signature);

-- migrate:down
DROP TABLE narinfo_signatures;
DROP TABLE narinfo_references;
ALTER TABLE narinfos DROP COLUMN ca;
ALTER TABLE narinfos DROP COLUMN system;
ALTER TABLE narinfos DROP COLUMN deriver;
ALTER TABLE narinfos DROP COLUMN nar_size;
ALTER TABLE narinfos DROP COLUMN nar_hash;
ALTER TABLE narinfos DROP COLUMN file_size;
ALTER TABLE narinfos DROP COLUMN file_hash;
ALTER TABLE narinfos DROP COLUMN compression;
ALTER TABLE narinfos DROP COLUMN url;
ALTER TABLE narinfos DROP COLUMN store_path;
