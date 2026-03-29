## Context

ncps currently uses three separate tools for database access:

- **sqlc** — generates engine-specific Go code from SQL query files (`db/query.*.sql`)
- **sqlc-multi-db** — merges those three generated codebases into a single `Querier` interface
- **dbmate** (wrapped in `nix/dbmate-wrapper`) — runs SQL migration files from `db/migrations/`; requires an external binary and nix packaging

This makes ncps harder to install (external dbmate binary), harder to maintain (three query files kept in sync), and harder to extend (any query change touches 3 files + regenerates 3 Go packages).

[Bun](https://github.com/uptrace/bun) is a full-featured Go ORM that supports SQLite, PostgreSQL, and MySQL/MariaDB from a single implementation, includes a migration runner (`bun/migrate`) that embeds SQL files into the binary, and integrates with `database/sql` (so the existing `otelsql` instrumentation layer is preserved).

## Goals / Non-Goals

**Goals:**
- Replace sqlc + sqlc-multi-db with `*bun.DB` used directly throughout the codebase
- Replace dbmate + dbmate-wrapper with `bun/migrate` (SQL files embedded via `go:embed`)
- Add `ncps migrate up|up-to|down|down-to|status` CLI sub-commands
- Preserve all existing database behaviour (same schema, same query semantics)
- Preserve OTel instrumentation via the existing `otelsql` wrapper
- Ensure tests cover every database operation before and after the migration

**Non-Goals:**
- Schema changes — table definitions are not modified in this change
- Switching to a Go-code migration format — existing `.sql` files will be re-used as-is inside `bun/migrate`
- ORM-style model associations or lazy loading — Bun is used as a query builder/runner, not a full ORM

## Decisions

### 1. Drop the `Querier` interface — pass `*bun.DB` directly

The existing `Querier` interface (`pkg/database/generated_querier.go`) and all generated wrapper code are deleted. Callers that currently accept a `database.Querier` are updated to accept `*bun.DB` instead. `*bun.DB` is the querier — there is no value in wrapping it behind a bespoke interface.

This removes:
- `generated_querier.go`
- `generated_wrapper_mysql.go`, `generated_wrapper_postgres.go`, `generated_wrapper_sqlite.go`
- `generated_errors.go`
- `generated_models.go` (replaced by hand-written `models.go`)
- `sqlitedb/`, `postgresdb/`, `mysqldb/` sub-packages entirely

`pkg/database.Open()` is kept as a thin constructor that parses the URL, opens the `*sql.DB` with `otelsql`, configures pragmas/pool, and wraps it in the correct Bun dialect — returning `*bun.DB`.

Transactions are handled via `db.RunInTx(ctx, opts, func(ctx, tx bun.Tx))` or by passing `bun.IDB` where callers need to work inside an existing transaction.

### 2. `bun/migrate` with embedded SQL files

Migration files remain as plain SQL files. `bun/migrate` requires each migration to be split into two files:

```
<version>_<name>.up.sql    # forward migration
<version>_<name>.down.sql  # rollback migration
```

Existing dbmate migrations are renamed to match this convention — the content is not changed. The `-- migrate:up` / `-- migrate:down` markers that dbmate uses within a single file become the file split.

They are embedded at build time:

```go
//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

//go:embed migrations/mysql/*.sql
var mysqlMigrations embed.FS
```

`bun/migrate` applies files in lexicographic order (matching the existing timestamp prefix). The wrapper binary (`nix/dbmate-wrapper`) is removed; `dbmate` itself is also removed from the dev shell and all nix packaging.

New migration files created during future development are plain `.up.sql` / `.down.sql` pairs using the same `YYYYMMDDHHmmss_<name>` timestamp prefix. The `/migrate-new` skill is updated to create the two files directly (e.g. with `touch`).

### 3. `ncps migrate` command in `pkg/ncps`

A new `migrate.go` file in `pkg/ncps` registers a `migrate` command with sub-commands:

| Sub-command  | Behaviour |
|---|---|
| `up`         | Apply all pending migrations |
| `up-to N`    | Apply migrations up to and including version N |
| `down`       | Roll back the last applied migration |
| `down-to N`  | Roll back to (but not including) version N |
| `status`     | Print all migrations and their applied/pending state |

The command accepts the same `--cache-database-url` flag used by `serve`. Connection setup reuses `pkg/database.Open()`.

### 4. Model structs with `bun` tags

Existing sqlc-generated models (`NarInfo`, `NarFile`, `Chunk`, `Config`, `PinnedClosure`) are rewritten as hand-maintained structs in `pkg/database/models.go` with Bun struct tags.

Every field on every model struct carries a `bun` tag — including fields where the tag is technically redundant (e.g. when the column name already matches the snake_case of the Go field name). This keeps the structs self-documenting and avoids relying on Bun's name-inference rules.

```go
type NarInfo struct {
    bun.BaseModel `bun:"table:narinfos,alias:ni"`

    ID             int64          `bun:"id,pk,autoincrement"`
    Hash           string         `bun:"hash,notnull"`
    CreatedAt      time.Time      `bun:"created_at,notnull"`
    UpdatedAt      sql.NullTime   `bun:"updated_at,nullzero"`
    LastAccessedAt sql.NullTime   `bun:"last_accessed_at,nullzero"`
    StorePath      sql.NullString `bun:"store_path,nullzero"`
    URL            sql.NullString `bun:"url,nullzero"`
    Compression    sql.NullString `bun:"compression,nullzero"`
    FileHash       sql.NullString `bun:"file_hash,nullzero"`
    FileSize       sql.NullInt64  `bun:"file_size,nullzero"`
    NarHash        sql.NullString `bun:"nar_hash,nullzero"`
    NarSize        sql.NullInt64  `bun:"nar_size,nullzero"`
    Deriver        sql.NullString `bun:"deriver,nullzero"`
    System         sql.NullString `bun:"system,nullzero"`
    Ca             sql.NullString `bun:"ca,nullzero"`
}
```

The same tagging discipline applies to all other model structs (`NarFile`, `Chunk`, `Config`, `PinnedClosure`, and junction models). Parameter structs (`CreateNarInfoParams`, etc.) are deleted along with the `Querier` interface — callers use Bun query builder calls directly.

### 5. Test-first approach

Before removing any sqlc-generated code, every database operation that lacks a direct test gets a black-box integration test in `pkg/database/` using the same helper infrastructure (`backends_test.go`, `contract_test.go`).

Tests must pass against **all three engines**. Run with:

```bash
eval "$(enable-integration-tests)"
go test -race ./pkg/database/...
```

SQLite always runs. PostgreSQL and MySQL run when the integration env vars are set (requires `nix run .#deps` to be running). The test suite is the regression net: if it passes on all three engines before the Bun rewrite and continues to pass after, the rewrite is correct.

### 6. Skills and documentation

The following skills are updated (not deleted and recreated — the symlink targets in `.agent/skills/` are edited in place):
- `dbmate` skill → replaced by `migrate-new` instructions using `touch`
- `sqlc` skill → removed; replaced by a `bun` skill documenting how to add queries
- `generate-db-wrappers` skill → removed (no more code generation step)
- `migrate-new`, `migrate-up`, `migrate-down` skills → updated for `ncps migrate`

`CLAUDE.md` is updated to reflect the new workflow. `docs/` pages mentioning dbmate or sqlc are updated.

## Risks / Trade-offs

| Risk | Mitigation |
|---|---|
| Bun query builder produces subtly different SQL for edge cases (e.g. `ON CONFLICT … WHERE`) | Pre-migration test suite catches regressions; complex queries use raw SQL via `bun.NewRaw` |
| Bulk insert helpers differ per dialect (`unnest` vs loop) | Single test matrix (SQLite + PG + MySQL) validates correct results for all engines |
| `bun/migrate` version tracking differs from dbmate's `schema_migrations` table | Migration files include a compatibility step that recognises the existing `schema_migrations` table |
| Binary size grows due to embedded SQL | Acceptable; migration files total ~50 KB across all engines |
| Callers updated from `Querier` to `*bun.DB` — wider blast radius | Mechanical change; compiler enforces completeness |
