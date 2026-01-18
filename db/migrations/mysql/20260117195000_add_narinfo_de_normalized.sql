-- migrate:up
-- Migration to add de-normalized NarInfo fields and helper tables

-- Add columns to narinfos table
ALTER TABLE narinfos ADD COLUMN store_path TEXT;
ALTER TABLE narinfos ADD COLUMN url TEXT;
ALTER TABLE narinfos ADD COLUMN compression VARCHAR(255);
ALTER TABLE narinfos ADD COLUMN file_hash VARCHAR(255);
ALTER TABLE narinfos ADD COLUMN file_size BIGINT UNSIGNED;
ALTER TABLE narinfos ADD COLUMN nar_hash VARCHAR(255);
ALTER TABLE narinfos ADD COLUMN nar_size BIGINT UNSIGNED;
ALTER TABLE narinfos ADD COLUMN deriver TEXT;
ALTER TABLE narinfos ADD COLUMN system VARCHAR(255);
ALTER TABLE narinfos ADD COLUMN ca TEXT;

-- Create references table
CREATE TABLE narinfo_references (
    narinfo_id BIGINT NOT NULL,
    reference VARCHAR(255) NOT NULL,
    PRIMARY KEY (narinfo_id, reference),
    CONSTRAINT fk_narinfo_references_narinfo_id FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
CREATE INDEX idx_narinfo_references_reference ON narinfo_references (reference);

-- Create signatures table
CREATE TABLE narinfo_signatures (
    narinfo_id BIGINT NOT NULL,
    signature VARCHAR(255) NOT NULL,
    PRIMARY KEY (narinfo_id, signature),
    CONSTRAINT fk_narinfo_signatures_narinfo_id FOREIGN KEY (narinfo_id) REFERENCES narinfos (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
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
