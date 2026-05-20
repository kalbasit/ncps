## Context

ncps today drives its database through three lockstep hand-maintained SQL files (`db/query.{sqlite,postgres,mysql}.sql`, ~67–69 queries each), a sqlc-multi-db wrapper that emits one Querier interface per engine (`pkg/database/generated_wrapper_*.go`), and `dbmate` migrations under `db/migrations/{sqlite,postgres,mysql}/` (8–14 files per engine, oldest from 2024). The hand-written `db/schema/*.sql` files (744 lines total) serve as dbmate's "current schema" record but are themselves hand-edited.

Pain points: every schema change touches six SQL files in lockstep; dialect drift is only caught at integration-test time; there is no compile-time link between the schema and the call site; and the migration history has diverged subtly across engines (e.g. SQLite has four extra early migrations the others were retroactively unified at `20260101000000_init_schema.sql`).

This change moves to Ent for schema-as-code, Atlas (as a Go library, not a CLI) for declarative migration generation, and Goose for the runtime migration applier. It also introduces a custom AST + SQL linter (`cmd/ent-lint`) to catch Ent codegen footguns at lint time rather than at integration-test time.

Existing production deployments have dbmate-applied schemas. They must continue to work without a manual data migration.

## Goals / Non-Goals

**Goals:**

- Single source of truth for the schema: Ent Go schemas under `ent/schema/`.
- Per-dialect versioned SQL migration files, integrity-verified by `atlas.sum`, embedded into the binary.
- Forward-only runtime migration via `goose.Provider` against the active DB URL.
- Continued first-class support for SQLite, PostgreSQL, and MySQL/MariaDB.
- Zero-downtime adoption from existing dbmate-applied databases.
- Static enforcement of the five Ent codegen invariants and the expand-contract policy via `cmd/ent-lint`, wired into `nix flake check`.
- Behaviour parity: the cache/server layers see the same CRUD semantics they do today.

**Non-Goals:**

- Schema-level changes to existing tables/columns/indexes/constraints (behaviour parity only).
- MySQL/MariaDB drop.
- Multi-DB / sharding (ncps remains single-DB).
- Migrate-down support.
- Replacing `pkg/cache/`, `pkg/server/`, or the transaction surface above `pkg/database/`.
- Live row migration tooling beyond what Atlas and Goose provide.
- Wiring `go-task` into CI (CI keeps its current `nix flake check` entry point; `task` is a developer convenience).

## Decisions

### D1: Ent (`entgo.io/ent`) for the ORM

Schemas live as Go structs under `ent/schema/*.go`. Fields, indexes, edges, mixins, and CHECK annotations are declarative. `go generate ./ent/...` runs `go tool ent generate` to produce the `ent/` client package. The client is committed.

**Why over sqlc-multi-db**: sqlc forces n × queries × dialects hand-maintained SQL. Ent generates dialect-aware DDL automatically. The downside (a small per-query ORM overhead) is acceptable because the hot streaming paths (NAR / NAR.zst) are `io.Reader`-based and do not pass through the ORM per byte.

**Why over `bun`**: `bun`'s SQLite dialect emits Postgres-style SQL for upserts and aggregates, requiring engine-specific workarounds. Ent's `dialect.SQLite | Postgres | MySQL` produces native DDL/DML for each dialect.

**Why over hand-written `database/sql`**: Loses compile-time guarantees, requires re-implementing transaction plumbing, no codegen drift check.

### D2: Atlas (`ariga.io/atlas`) consumed as a Go library

`cmd/generate-migrations` imports `ariga.io/atlas/sql/sqltool` and `entgo.io/ent/dialect/sql/schema`. For each dialect, it opens an in-memory or dev database, replays the existing migration history (`schema.ModeReplay`), then diffs the result against the Ent schema's `migrate.Tables` and writes a new Goose-formatted file to `migrations/<dialect>/`.

**Why library, not CLI**: No external binary to package in Nix or Docker; reproducible from `go.mod` pins alone; `atlas` releases that break the library API surface (semver-controlled) are easier to manage than CLI flag drift. The reference repo proves the library API is sufficient.

### D3: Goose (`pressly/goose/v3`) as the runtime migrator

`ncps migrate up` opens the configured database, picks a goose dialect from the URL scheme, mounts the embedded `migrations/<dialect>/` subtree via `fs.Sub`, and calls `provider.Up(ctx)`.

**Why Goose over Ent's `Schema.Create`**: `Schema.Create` runs DDL at app boot and has no versioning. Goose persists applied versions in a tracking table — necessary for staged backfills (the four-step NOT NULL recipe) and for reconciling with prior dbmate state.

**Why over dbmate**: dbmate is an external binary requiring the wrapper in `nix/dbmate-wrapper/`; Goose is an in-process library. We also need programmatic access to the tracking table state for the dbmate-adoption hook (D7).

**Tracking table name**: `ncps_schema_versions` (single fixed name across engines, set via `goose.WithTableName`). Avoids colliding with dbmate's `schema_migrations`.

### D4: Migration directory layout — no shard subdirectory

```
migrations/
├── sqlite/
│   ├── YYYYMMDDHHMMSS_<name>.sql
│   └── atlas.sum
├── postgres/
│   ├── YYYYMMDDHHMMSS_<name>.sql
│   └── atlas.sum
├── mysql/
│   ├── YYYYMMDDHHMMSS_<name>.sql
│   └── atlas.sum
└── migrations.go        // //go:embed sqlite/* postgres/* mysql/*
```

No `migrations/<shard>/<dialect>/` layer — ncps has one logical database. The single `embed.FS` is split into per-dialect sub-FS at runtime via `fs.Sub(MigrationsFS, dialect)`.

### D5: Single Ent client; dialect chosen at runtime

`pkg/database/` exposes one Ent client. The dialect string (`dialect.SQLite | Postgres | MySQL`) is derived from the URL scheme and passed via `entsql.OpenDB(dialect, *sql.DB)`. There is no per-engine generated package — Ent's runtime handles SQL dialect dispatch.

**Why not three clients (one per engine)**: The reference uses one client per *logical database*, not one per *engine*. ncps has one logical DB, so one client. Three engines × one client = three drivers, not three clients.

### D6: Reconciliation — translate dbmate migrations 1:1 and keep the `schema_migrations` table

ncps installations in the wild span many versions (a v0.4 install may have applied only a subset of the SQLite-era migrations). We cannot assume "table exists ⇒ on the latest version". Goose's per-file tracking is exactly what we need — the tracking table tells goose which versions are applied; goose runs only the missing ones.

The challenge is that dbmate's `schema_migrations` shape (`version VARCHAR PRIMARY KEY`) is incompatible with goose's default (`id, version_id, is_applied, tstamp`). We bridge this with a one-shot in-place adoption ALTER and keep dbmate's table name forever.

**Migration files**: Each existing dbmate file under `db/migrations/<dialect>/` is translated mechanically to a goose file under `migrations/<dialect>/`, preserving the original timestamp:
- `-- migrate:up` → `-- +goose Up`
- `-- migrate:down` → `-- +goose Down`
- DDL body verbatim
- SQLite's 14-file history and Postgres/MySQL's 8-file histories are preserved as-is; the dialects converge naturally at `20260101000000_init_schema.sql` and after, exactly as they do today.

**Tracking table**: Stay on `schema_migrations` (dbmate's name). Goose is configured with `goose.WithTableName("schema_migrations")`. One identifier across fresh and adopted installs. Eliminates parallel tables, operator-script churn, and special-case branching in code that would otherwise check "which table do I look at?".

**End state**: every install — fresh or adopted — has `schema_migrations` with goose's canonical 4-column schema (`id` PK, `version_id`, `is_applied`, `tstamp`). No `version` column. No half-populated columns. Operators see a single coherent table.

**Adoption flow** runs once at the top of `ncps migrate up`, before handing control to goose. The state-detection probe and the executed action are dialect-asymmetric because MySQL does not support transactional DDL.

#### SQLite + Postgres (transactional, atomic)

Both dialects support transactional DDL, so the entire adoption is a single transaction. There are no intermediate states to recover from.

State detection: `column_exists("schema_migrations", "is_applied")`.

- **Table doesn't exist** → fresh install. Goose creates `schema_migrations` in its own schema (using the dbmate name). All translated migrations run from scratch.
- **`is_applied` column exists** → already adopted. Hand off to goose.
- **`is_applied` column missing** → dbmate-format. Execute:

  ```sql
  BEGIN;
  CREATE TEMPORARY TABLE schema_migrations_legacy AS SELECT version FROM schema_migrations;
  DROP TABLE schema_migrations;
  CREATE TABLE schema_migrations (
    id          <serial-pk>,           -- INTEGER PRIMARY KEY AUTOINCREMENT (sqlite) / SERIAL PRIMARY KEY (postgres)
    version_id  BIGINT  NOT NULL,
    is_applied  BOOLEAN NOT NULL,
    tstamp      TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP
  );
  INSERT INTO schema_migrations (version_id, is_applied, tstamp)
    SELECT CAST(version AS BIGINT), TRUE, CURRENT_TIMESTAMP
    FROM schema_migrations_legacy;
  -- verify: row count parity
  -- (assert via a separate SELECT in Go before COMMIT)
  COMMIT;
  ```

  If the verify assertion fails, the transaction rolls back and adoption is retried on next boot. No half-state is observable to operators.

#### MySQL/MariaDB (non-transactional, idempotent with state recovery)

DDL is auto-committed per statement, so we use a rename-then-rebuild pattern with a backup table that survives crashes for resumption.

State probe at boot:

| `schema_migrations` exists? | `..._dbmate_backup` exists? | `is_applied` on `schema_migrations`? | State |
|---|---|---|---|
| no  | no  | —   | **S1**: fresh install — goose handles |
| yes | no  | yes | **S2**: adopted |
| yes | no  | no  | **S3**: not started |
| no  | yes | —   | **S4**: crashed after RENAME, before CREATE |
| yes | yes | yes | **S5**: crashed after CREATE/INSERT, before DROP backup |
| yes | yes | no  | **S6**: impossible (RENAME would have failed); abort with diagnostic |

Action per state:

- **S2**: hand off to goose.
- **S3**: full sequence:
  1. `ALTER TABLE schema_migrations RENAME TO schema_migrations_dbmate_backup`
  2. `CREATE TABLE schema_migrations (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, version_id BIGINT NOT NULL, is_applied TINYINT(1) NOT NULL, tstamp TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP)`
  3. `INSERT INTO schema_migrations (version_id, is_applied, tstamp) SELECT CAST(version AS UNSIGNED), TRUE, CURRENT_TIMESTAMP FROM schema_migrations_dbmate_backup`
  4. Verify row-count parity (Go-side `SELECT COUNT(*)` against both tables).
  5. `DROP TABLE schema_migrations_dbmate_backup`
- **S4**: resume from step 2 (CREATE + INSERT + DROP backup).
- **S5**: verify row-count parity between the two tables.
  - If equal: just `DROP TABLE schema_migrations_dbmate_backup`.
  - If unequal: `TRUNCATE TABLE schema_migrations`, re-INSERT from backup, re-verify, then DROP backup.
- **S6**: abort with operator diagnostic; this state should be unreachable and indicates manual intervention or corruption.

**Why `is_applied` is the right sentinel**: it is NOT NULL in goose's schema (so its presence is unambiguous), and operator monitoring queries are likely to reference it, making accidental schema drift detectable.

**Why not a custom `goose.Store`**: a Store implementation keeps the code working against dbmate's schema indefinitely, but it's bespoke Go to maintain across goose's major-version bumps and prevents the convergent-tracking-table goal. A one-time rebuild is well-understood DDL, and after adoption every install runs the standard goose code path.

**Why drop the `version` column rather than keep it for downgrade safety**: operators reading `schema_migrations` post-upgrade would see a column populated only for historical rows and empty/NULL for new ones — confusing and bug-suggestive. Downgrade-to-dbmate is not a supported operator workflow (restore from backup is). The clean-final-shape benefit dominates.

**Why not require a manual operator migration**: ncps is self-hosted; there is no script-delivery mechanism. The adoption must be automatic and idempotent.

### D7: MySQL/MariaDB validation

The reference repo handles only sqlite + postgres. ncps must validate three points before relying on the pipeline for MySQL:

1. `entsql.Open("mysql", url)` and `dialect.MySQL` produce correct DDL for the existing schema (Ent's documented behaviour, but unproven in this combination locally).
2. `ariga.io/atlas/sql/sqltool.GooseFormatter` emits MySQL-syntactic DDL (Atlas docs claim yes; verify by inspecting a generated baseline).
3. `goose.DialectMySQL` correctly applies the generated baseline against MariaDB 11 (the version `process-compose-flake` ships).

If any fails, fall back is: use Ent's MySQL dialect for codegen + DDL diff, but write a small custom Goose formatter (≈50 LOC) that emits MySQL-quoted identifiers. Decision deferred until the spike in Tasks §1.

### D8: `cmd/generate-migrations` shape

```
go run ./cmd/generate-migrations --name <descriptive_name>
go run ./cmd/generate-migrations --sql-only --name <descriptive_name>
```

Without `--sql-only`, runs the diff for **all three dialects in one invocation** (one timestamp prefix shared across the three files). With `--sql-only`, writes empty Goose stubs (`-- +goose Up\n\n-- +goose Down\n`) to each dialect directory and exits.

**Why one invocation, not per-dialect**: keeps the three dialect files chronologically aligned. The reference does per-dialect because it has three shards × two dialects = six independent files; ncps has one shard × three dialects and benefits from atomic generation.

**Name validation**: reject empty, `auto`, `wip`, `tmp`, and names containing spaces. Match the rule's "no placeholder names" requirement.

### D9: `cmd/ent-lint` shape

A Go binary that:

- Parses `ent/schema/*.go` via `go/parser` and walks the AST to enforce the five codegen invariants (A1–A5) plus the snake_case enum-type convention.
- Reads the newest file in each `migrations/<dialect>/` directory and applies a regex/AST-style check for forbidden DDL (`DROP COLUMN`, `DROP TABLE`, `RENAME`, `ADD COLUMN … NOT NULL` without `DEFAULT` against a non-empty-table context).
- Reads all `migrations/<dialect>/*.sql` baselines and verifies every CHECK from the schema's `Annotations()` appears in both dialects (no silent drops).
- Exits non-zero with a checklist-formatted report on any failure.

Wired into `nix flake check` as a new `ent-lint-check` and into the `task ent:check` chain for local use.

**Invariant A5 (`*_ciphertext` → `.Sensitive()`)** is dormant for ncps today (no encrypted columns), but enforced so future encrypted fields cannot regress silently.

### D10: Forward-only — no `migrate down`

`ncps migrate down` exits with an error pointing operators at the expand-contract and four-step NOT NULL recipes.

**Why**: Goose technically supports down migrations, but most production-relevant schema changes (`DROP COLUMN`, `DROP NOT NULL`, `DROP INDEX`) are not safely reversible against live data. Down migrations encourage a false sense of safety. The expand-contract recipe is the operator-correct alternative.

### D11: `pkg/database/` surface

- `database.Open(url, *PoolConfig)` returns `*database.Client` (an Ent client + driver metadata) instead of `Querier`.
- `database.Client` exposes the generated Ent fluent API directly (e.g. `client.NarInfo.Create().Set...().Save(ctx)`).
- Transactions: `client.Tx(ctx)` returns `*ent.Tx`; the cache layer's existing `withTransaction(name string, fn func(qtx) error)` wrapper is preserved by reshaping its closure parameter to `*ent.Tx`.
- The current `Querier` interface, `generated_models.go`, `generated_errors.go`, `generated_wrapper_*.go`, and per-engine adapter packages (`mysqldb/`, `postgresdb/`, `sqlitedb/`) are deleted.
- `database.DetectFromDatabaseURL` and `database.PoolConfig` stay (URL handling and pool tuning are still cross-cutting concerns).

### D12: Removal list

- `db/query.{sqlite,postgres,mysql}.sql`
- `db/schema/{sqlite,postgres,mysql}.sql`
- `db/migrations/{sqlite,postgres,mysql}/*` (after the baseline is committed)
- `pkg/database/generated_*.go`, `pkg/database/{mysqldb,postgresdb,sqlitedb}/`
- `nix/dbmate-wrapper/`
- `.agent/skills/{sqlc,generate-db-wrappers}`
- `go.mod` `tool` entry for `github.com/kalbasit/sqlc-multi-db`

### D13: Test strategy (TDD)

Per the change's TDD rule, every step lands tests first:

- **Schema parity tests** (run before any Ent schema is written): integration tests that assert specific tables, columns, indexes, FKs, and CHECKs exist in a freshly-migrated DB. These are run against the *current* dbmate-applied schema before the migration begins; they must pass unchanged after the Ent baseline lands.
- **Ent-lint tests**: each invariant has a positive (good schema) and negative (bad schema) fixture under `cmd/ent-lint/testdata/`. The lint binary is exercised against the fixtures.
- **Adoption integration tests** (per engine): seed a database to a *partial* dbmate state (run only the first 3, then only the first 7, then all 8/14 dbmate migrations), run `ncps migrate up`, assert: (a) `schema_migrations` now has goose's 4-column shape and no `version` column, (b) all originally-applied versions appear as rows with `is_applied=true` and `version_id` matching the dbmate timestamp, (c) no historical DDL was re-executed (use a sentinel row inserted between migrations + table-size invariant), (d) any *newer* translated migration files were applied. Specifically cover the "v0.4-era install" case for SQLite (early SQLite-only migrations applied, none of the unified ones).
- **Adoption idempotency test** (per engine): run `migrate up` twice in a row against the same DB; the second run must be a no-op (assert no DDL via dialect-specific introspection on `information_schema` / `pgcatalog` / `sqlite_master`).
- **SQLite + Postgres rollback-on-failure test**: inject a verify-step failure (e.g. mismatched row counts via test hook) and assert the transaction rolls back — `schema_migrations` retains dbmate shape, no temp table leaks, retry on next boot succeeds.
- **MySQL state-machine recovery tests**: exhaustively cover S3 → S4 (crash after RENAME), S3 → S5-equal (crash after INSERT, before DROP backup), and S3 → S5-unequal (crash mid-INSERT). For each, kill the connection at the relevant point, then re-run `migrate up` and assert the final state matches the happy-path outcome. Add a negative test for S6 (manually create both tables in the impossible shape) that asserts `migrate up` exits with the operator diagnostic.
- **Forward migration test**: starting from the post-adoption state, apply a follow-up additive migration (added via a temp test fixture) and assert it lands cleanly under all three engines.
- **Schema-equivalence golden test**: after applying all translated migrations to a fresh DB, run `atlas migrate diff` against the Ent schema; the diff must be empty for every dialect. This is the load-bearing guarantee that the 1:1 translations didn't drift.
- **Generate-migrations golden test**: snapshot the newest generated `.sql` per dialect and compare on each PR — drift means a schema change slipped through.

## Risks / Trade-offs

- **MySQL is the riskiest dialect** (no prior in-house evidence) → mitigate with an upfront spike in Tasks §1 before committing to the wider refactor. If the Goose formatter doesn't emit MySQL-correct DDL, fall back to a ~50-LOC custom formatter rather than dropping MySQL.
- **Adoption is one-shot and load-bearing** — a bug there bricks an upgrading deployment → mitigate with the per-engine + partial-state integration tests in D13, idempotency tests, the SQLite/Postgres rollback-on-failure test, the MySQL state-machine recovery tests, a `--dry-run` flag on `migrate up` that prints the state-detection result without touching the schema, and a `CHANGELOG.md` note advising a DB backup before upgrade.
- **MySQL adoption is non-atomic** — the rename / create / insert / drop sequence has crash windows → mitigated by the explicit state machine (S1–S6) with idempotent recovery, the backup table preserves the source rows until the new table is verified, and dedicated tests exercise each crash window.
- **dbmate-translation fidelity** — a misplaced `;` or dialect-specific DDL quirk during the mechanical translation could silently change schema semantics → mitigate with the schema-equivalence golden test (post-translation schema must produce a zero-byte diff against Ent's expected schema) gated in CI for every dialect.
- **Ent ORM overhead on hot paths** → mitigate by keeping NAR streaming I/O outside the ORM and benchmarking `GetNarInfo` / `PutNarInfo` before/after to confirm the regression is in the noise (<5% wall-clock per request).
- **Atlas is pinned at a pre-release** (`v0.36.2-0.20250730…`) → mitigate by pinning the exact commit in `go.mod`; revisit on every dependabot bump; track upstream for the stable v0.36.x or v0.37 cut.
- **Loss of migrate-down** is a real operator capability loss → mitigate via documented expand-contract recipe, the four-step NOT NULL procedure, and an explicit pointer in the error message.
- **Dormant A5 invariant** (`.Sensitive()`) is enforced on a column type ncps doesn't yet use → mitigate by keeping the rule active so any future encrypted column cannot regress silently; no behaviour cost today.
- **Goose tracking table coexists with dbmate's `schema_migrations`** post-upgrade → mitigate by documenting it as expected and by leaving dbmate's table in place for the audit trail; a future change may add a cleanup.

## Migration Plan

1. **MySQL spike** (1–2 days, no production impact): prove that Ent + Atlas library + Goose formatter handle MySQL end-to-end for one toy table. Decision gate: proceed as planned, or add the custom formatter shim.
2. **Land Ent schemas** matching the current DDL exactly (column types, nullability, defaults, indexes, FKs, CHECKs). No call-site changes yet.
3. **Translate the dbmate migration files 1:1** into `migrations/<dialect>/` with preserved timestamps. Run all of them against a fresh DB and assert the schema matches Ent's expected output (the schema-equivalence golden test). No new content is added in this step — only header-directive rewrites.
4. **Add `cmd/generate-migrations`** so the next schema change can be authored via `task migrations:gen NAME=…`. Verify that running it against the post-translation state produces zero diff (no spurious migration files).
5. **Add `cmd/ent-lint`** with full test fixtures.
6. **Wire `nix flake check`**: add codegen-drift check, ent-lint check, atlas-sum integrity check, schema-equivalence golden check, and the existing test suite continues to gate.
7. **Add `cmd/ncps migrate up`** with the adoption ALTER and `--dry-run` flag. Integration tests cover fresh installs, fully-adopted installs, partial v0.4-era dbmate installs (SQLite), and the MySQL crash-recovery path.
8. **Reshape `pkg/database/`** around the Ent client; convert callers in `pkg/cache/`, `pkg/ncps/`, `pkg/server/`, `cmd/` in sequence. Each conversion lands with its tests.
9. **Delete** sqlc + dbmate artifacts (D12 list).
10. **Update** `CLAUDE.md`, `.agent/skills/migrate-*`, README, dev-shell.

**Rollback**: each step is a separate PR. Steps 2–6 are pure additions and trivially reversible. Step 7 is the first deploy-affecting change. After adoption runs on a customer DB, downgrading to a dbmate-using ncps binary is *not* supported — the recovery path for a broken upgrade is "restore from backup", which is the operator workflow `CHANGELOG.md` will document. This is an explicit trade-off against keeping the `version` column for downgrade compatibility; the operator-clarity win was judged dominant. Steps 8–10 are post-cutover cleanup, revertible until step 9 is merged.

## Open Questions

- **Atlas version pin**: stay on the pre-release the reference uses, or wait for a stable Atlas release? Track upstream for the next 30 days and decide at step 1.
- **`migrate up --dry-run`**: surface (a) pending migrations and (b) whether adoption is needed — confirmed in scope per the adoption-ALTER risk mitigation.
- **Embed pattern**: `//go:embed sqlite/* postgres/* mysql/*` per-dialect vars vs one var with `fs.Sub`. Recommended: one var (`MigrationsFS`) + `fs.Sub` — simpler, one source of truth.
- **`db/` directory retention**: does anything outside migrations live there long-term, or do we move to `migrations/` at repo root? Recommended: `migrations/` at repo root; `db/` becomes legacy/empty and is removed.
- **CDC and pinned-closures tables**: spot-check the most recent dbmate migrations (CDC chunk tables, `verified_at`, `pinned_closures`) translate cleanly. These are the migrations most likely to expose dialect quirks because they're the newest.
