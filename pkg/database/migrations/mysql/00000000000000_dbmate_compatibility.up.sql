-- Migration: Copy dbmate schema_migrations to bun_migrations if table exists with records
-- For new databases (no schema_migrations): bun_migrations auto-created by bun, nothing to do
-- For existing dbmate databases: migrate records and drop schema_migrations
-- This migration is idempotent and safe to run multiple times

DROP PROCEDURE IF EXISTS migrate_dbmate;

DELIMITER //

CREATE PROCEDURE migrate_dbmate()
BEGIN
    DECLARE table_exists INT DEFAULT 0;

    -- Check if schema_migrations table exists
    SELECT COUNT(*) INTO table_exists
    FROM information_schema.tables
    WHERE table_schema = DATABASE()
    AND table_name = 'schema_migrations';

    IF table_exists > 0 THEN
        -- Table exists - migrate any records to bun_migrations
        INSERT IGNORE INTO bun_migrations (name, migrated_at)
        SELECT version, COALESCE(migrated_at, CURRENT_TIMESTAMP)
        FROM schema_migrations
        WHERE version IS NOT NULL;

        -- Drop schema_migrations after migration (no longer needed; bun/migrate uses bun_migrations)
        DROP TABLE IF EXISTS schema_migrations;
    END IF;
    -- If table doesn't exist (new database): nothing to do, bun_migrations auto-created by bun
END//

DELIMITER ;

CALL migrate_dbmate();
DROP PROCEDURE IF EXISTS migrate_dbmate;
