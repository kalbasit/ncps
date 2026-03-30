-- Migration: Copy dbmate schema_migrations to bun_migrations if table exists with records
-- For new databases (no schema_migrations): bun_migrations auto-created by bun, nothing to do
-- For existing dbmate databases: migrate records and drop schema_migrations
-- This migration is idempotent and safe to run multiple times

-- Use temp table to safely check if schema_migrations existed (dbmate database)
CREATE TEMPORARY TABLE IF NOT EXISTS _dbmate_check (existed INTEGER);
INSERT INTO _dbmate_check
SELECT 1 FROM sqlite_master
WHERE type = 'table' AND name = 'schema_migrations';

-- Only migrate if schema_migrations existed and has records
INSERT INTO bun_migrations (name, migrated_at)
SELECT
    schema_migrations.version,
    COALESCE(schema_migrations.migrated_at, CURRENT_TIMESTAMP)
FROM schema_migrations, _dbmate_check
WHERE _dbmate_check.existed = 1
AND NOT EXISTS (
    SELECT 1 FROM bun_migrations bm
    WHERE bm.name = schema_migrations.version
);

-- Clean up: drop schema_migrations if it existed (no longer needed after migration)
DROP TABLE IF EXISTS schema_migrations;
DROP TABLE IF EXISTS _dbmate_check;
