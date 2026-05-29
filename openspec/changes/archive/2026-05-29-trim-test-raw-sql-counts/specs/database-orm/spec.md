## ADDED Requirements

### Requirement: Standalone row-count assertions in behavior tests use the Ent client

Where a cache or server behavior test makes a **standalone** row-count assertion — one that is not interleaved with raw `*sql.DB` setup in the same flow — it SHALL obtain the count through the Ent client (`dbClient.Ent().<Entity>.Query()[.Where(entX.HashEQ(h))].Count(ctx)`) rather than via a raw `SELECT COUNT(*)` string. Raw `*sql.DB` access in tests (via `Client.DB()`) SHALL be reserved for scenarios where it is the right tool: verifying the outcome of data migrations, seeding adversarial/invalid rows the ORM would reject, inspecting persistence-layer columns, connection-pool tuning, schema/catalog probes, database-admin operations, and count assertions that are interleaved with such raw-SQL mutation or `Eventually`-polling logic within the same test flow.

#### Scenario: Standalone count assertion uses Ent

- **WHEN** a cache or server behavior test makes a standalone assertion about the number of rows in a table (optionally filtered by hash) and does not interleave raw `*sql.DB` access in that flow
- **THEN** it SHALL obtain the count via `dbClient.Ent().<Entity>.Query()[.Where(entX.HashEQ(h))].Count(ctx)`
- **AND** it SHALL NOT issue a raw `SELECT COUNT(*)` through `Client.DB()`

#### Scenario: Raw SQL retained where it is the right tool

- **WHEN** a test verifies a data-migration outcome, seeds an invalid/adversarial row, inspects persistence-layer timestamps, tunes the connection pool, probes schema shape, or makes a count assertion interleaved with raw-SQL mutations/polling in the same flow
- **THEN** it MAY continue to use `Client.DB()` raw SQL
- **AND** any count assertion that is converted to Ent SHALL produce identical pass/fail outcomes to the raw-SQL version it replaced
