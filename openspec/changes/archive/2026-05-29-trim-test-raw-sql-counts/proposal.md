## Why

`Client.DB()` is documented as a discouraged raw-`*sql.DB` escape hatch, yet the test suites reach for it ~110 times. Most of those uses are legitimate (migration-outcome verification, schema-shape probes, adversarial state setup, connection-pool tuning). But one pattern is a pure clean swap that loses nothing by moving to the Ent API: bare table row-count assertions (`SELECT COUNT(*) FROM <table>`) in the cache and server *behavior* tests. Converting them removes hand-written SQL strings from behavior assertions and shrinks the raw-SQL surface to only what genuinely benefits from ORM-independent verification.

## What Changes

- Convert the **standalone** `SELECT COUNT(*)` row-count assertions to the Ent API (`dbClient.Ent().<Entity>.Query()[.Where(HashEQ(h))].Count(ctx)`): the 5 bare table-count assertions in `pkg/cache/cache_test.go` and all 5 by-hash count assertions in `pkg/server/server_test.go` (which are clean standalone behavior checks).
- **Retain** the by-hash `COUNT(*)` assertions in `pkg/cache/cache_test.go` that are interleaved with raw-SQL idioms in the same flow — the `rebind()` placeholder helper, adjacent `ExecContext` `DELETE`/`UPDATE` mutations, `Eventually`-polling loops, and concurrency (`wg`) verification. Converting only the COUNT while the surrounding flow stays raw would mix styles within one test; raw SQL is the right tool there.
- Explicitly **retain raw `DB()` SQL** everywhere it is the right tool, and document why:
  - `pkg/ncps/migrate_*_test.go` — verify the *outcome of data migrations*; ORM-independent verification is the point.
  - `ExecContext` mutations that set up adversarial/invalid state (NULL URL, `total_chunks=0`, manual `DELETE`/`UPDATE`) that Ent would reject or obscure.
  - Multi-column timestamp inspections (`created_at`, `last_accessed_at`) that assert persistence-layer behavior.
  - `SetMaxOpenConns`, `Close()`, schema/`information_schema`/PRAGMA probes, and the `testhelper` `CREATE/DROP DATABASE` admin paths.
  - `pkg/database/client_test.go`'s `assert.Same(sdb, c.DB())`, which tests `DB()` itself.
- The production `pkg/ncps/migrate.go` use of `DB()` (goose needs raw `*sql.DB`) is unchanged.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `database-orm`: add a requirement that behavior tests assert row presence/counts through the Ent client, with raw `*sql.DB` test access reserved for migration/schema/adversarial scenarios.

## Impact

- Code: `pkg/cache/cache_test.go` (5 sites), `pkg/server/server_test.go` (5 sites). Test-only; no production code change.
- No schema/migration/runtime change.
- Tests: the converted assertions must produce identical pass/fail outcomes.

## Non-goals

- Not eliminating `DB()` or converting migration-verification, adversarial-setup, timestamp-inspection, pool-tuning, or schema-probe sites.
- Not touching `pkg/ncps`, `testhelper`, or `pkg/database/client_test.go`.

## I/O, latency, memory impact

None. Test-only; `COUNT(*)` becomes Ent's equivalent `Count(ctx)`, issuing the same aggregate query.
