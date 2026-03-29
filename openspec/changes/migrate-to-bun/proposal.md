## Why

The current database stack (sqlc + sqlc-multi-db + dbmate + dbmate-wrapper) requires three separate query files, generated code across three engines, and an external `dbmate` binary to run migrations — creating a heavy installation burden and operational complexity. Replacing it with [Bun](https://github.com/uptrace/bun) consolidates all database operations into a single Go library with migrations embedded in the binary, turning `ncps migrate up` into the only command users need.

## What Changes

- **BREAKING**: Remove `sqlc`, `sqlc-multi-db`, and all `db/query.*.sql` files; delete all `generated_*.go` files and the `Querier` interface.
- **BREAKING**: Remove `dbmate`, `nix/dbmate-wrapper`, and all dbmate invocations (Nix modules, scripts, skills, CLAUDE.md).
- Add `github.com/uptrace/bun` (+ `bun/driver/sqliteshim`, `bun/dialect/pgdialect`, `bun/dialect/mysqldialect`, `bun/migrate`) as the sole database abstraction layer.
- Rename existing SQL migration files to `bun/migrate`'s `.up.sql` / `.down.sql` convention and embed them in the binary via `go:embed`.
- Annotate all database model structs with `bun` tags; remove sqlc-generated types.
- Add `pkg/ncps` command `migrate` with sub-commands: `up`, `up-to`, `down`, `down-to`, `status`.
- Rewrite all database adapter calls (currently in `pkg/database/`) using Bun query builder / raw SQL.
- Add tests for every database adapter function before rewriting, to ensure behaviour is preserved.
- Update `.agent/skills/` (dbmate, sqlc, generate-db-wrappers, migrate-new/up/down), CLAUDE.md, and `docs/` to reflect the new workflow.

## Capabilities

### New Capabilities

- `database-migrations`: Embedded binary migrations with `bun/migrate`; `ncps migrate up|up-to|down|down-to|status` replaces the external `dbmate` workflow.
- `bun-database-layer`: `*bun.DB` used directly as the database handle throughout the codebase; Bun struct tags on model structs; query builder replaces sqlc-generated code.

### Modified Capabilities

- `data-model`: Bun struct tags are added to all model types; sqlc-generated structs are removed. The relational schema is unchanged but Go representation changes.
- `architecture`: The external `dbmate` binary dependency is removed; migrations are now part of the ncps binary itself. Affects installation docs and Nix packaging.

## Impact

- **Code**: `pkg/database/` fully rewritten; `pkg/ncps/` gains `migrate` command; `cmd/` updated for new sub-command.
- **Dependencies**: `go.mod` loses `github.com/kalbasit/sqlc-multi-db`, `sqlc` tooling; gains `github.com/uptrace/bun`.
- **Nix**: Remove `nix/dbmate-wrapper/`, update dev shell, `nix/packages/`, and `nix/process-compose/` (no more dbmate in `preCheck`).
- **Skills / docs**: `dbmate`, `sqlc`, `generate-db-wrappers`, `migrate-new`, `migrate-up`, `migrate-down` skills updated or removed; CLAUDE.md rewritten for new workflow.
- **No schema changes**: Existing database tables and column definitions are preserved as-is.
- **Performance**: Negligible; Bun adds <1 ms per query vs sqlc raw queries. Binary size increases slightly due to embedded migrations.
