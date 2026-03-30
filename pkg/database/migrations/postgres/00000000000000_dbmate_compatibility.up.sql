-- Migration: Copy dbmate schema_migrations to bun_migrations if table exists with records
-- For new databases (no schema_migrations): bun_migrations auto-created by bun, nothing to do
-- For existing dbmate databases: migrate records and drop schema_migrations
-- This migration is idempotent and safe to run multiple times

DO $$
BEGIN
    -- Check if schema_migrations table exists and has records
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'schema_migrations'
    ) AND EXISTS (SELECT 1 FROM schema_migrations LIMIT 1) THEN
        -- Migrate records to bun_migrations
        INSERT INTO bun_migrations (name, migrated_at)
        SELECT version, COALESCE(migrated_at, CURRENT_TIMESTAMP)
        FROM schema_migrations
        WHERE NOT EXISTS (
            SELECT 1 FROM bun_migrations bm
            WHERE bm.name = schema_migrations.version
        )
        ON CONFLICT DO NOTHING;

        -- Drop the dbmate table after migration (no longer needed)
        DROP TABLE IF EXISTS schema_migrations;
    END IF;
    -- If table doesn't exist (new database): nothing to do, bun_migrations auto-created by bun
END $$;
