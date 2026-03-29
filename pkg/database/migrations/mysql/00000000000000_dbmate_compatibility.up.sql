-- This migration provides compatibility between dbmate and bun/migrate.
-- For existing dbmate databases, it copies migration records from schema_migrations
-- to bun_migrations so already-applied migrations are not re-run.
-- For new databases, this migration creates the schema_migrations table if needed
-- and does nothing else.
--
-- This migration is idempotent and safe to run multiple times.
-- NOTE: The bun_migrations table is created automatically by bun/migrate.

-- Create schema_migrations table if it doesn't exist (for new databases).
-- Dbmate creates this with columns: version (PK), migrated_at
CREATE TABLE IF NOT EXISTS schema_migrations (
    version varchar(128) primary key,
    migrated_at timestamp null
);

-- Copy any existing migration records from dbmate's schema_migrations table
-- to bun_migrations. Use INSERT IGNORE to skip if already exists.

INSERT IGNORE INTO bun_migrations (name, migrated_at)
SELECT
    schema_migrations.version,
    COALESCE(schema_migrations.migrated_at, CURRENT_TIMESTAMP)
FROM schema_migrations
WHERE schema_migrations.version IS NOT NULL;
