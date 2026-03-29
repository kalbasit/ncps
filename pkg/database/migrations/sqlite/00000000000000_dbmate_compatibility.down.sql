-- Reverse the dbmate compatibility layer.
-- This deletes the migration records we copied from schema_migrations
-- and drops the bun_migrations table. Note: this means migration history
-- tracked by dbmate will no longer be recognized by bun/migrate after this rollback.
DELETE FROM bun_migrations
WHERE name IN (SELECT version FROM schema_migrations);

DROP TABLE IF EXISTS bun_migrations;
