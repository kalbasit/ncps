-- Reverse the dbmate compatibility layer.
-- Recreates schema_migrations from bun_migrations if rollback to dbmate is needed.
-- NOTE: This means migration history tracked by bun/migrate will no longer be
-- recognized after this rollback.

-- Recreate schema_migrations table
CREATE TABLE IF NOT EXISTS schema_migrations (
    version varchar(128) PRIMARY KEY,
    migrated_at timestamp
);

-- Copy records from bun_migrations back to schema_migrations
INSERT INTO schema_migrations (version, migrated_at)
SELECT name, migrated_at FROM bun_migrations;

-- Drop bun_migrations (we've reverted to dbmate state)
DROP TABLE IF EXISTS bun_migrations;
