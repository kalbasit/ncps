## MODIFIED Requirements

### Requirement: Package Structure
The package structure is updated to remove sqlc sub-packages, dbmate wrapper, and query files. `pkg/database/` becomes a flat package with hand-written Bun-based code. A `pkg/ncps/` package gains the `migrate` command.

```
cmd/                        CLI entrypoints (serve, migrate-narinfo, migrate, global flags, OTel bootstrap)
pkg/
  cache/                    Core cache logic: fetch, store, evict, CDC, LRU
    upstream/               Upstream cache HTTP client
    healthcheck/            Health-check helpers
  server/                   HTTP server (chi router), handler functions
  database/                 Database layer: *bun.DB construction, model structs, Bun queries
  ncps/                     CLI commands (serve, migrate, migrate-narinfo)
  storage/
    local/                  Local filesystem NarInfoStore + NarStore
    s3/                     S3-compatible NarInfoStore + NarStore (MinIO)
    chunk/                  chunk.Store implementations (local + S3)
  nar/                      NAR URL parsing, hash validation, compression types
  narinfo/                  narinfo parsing helpers (re-exports go-nix library types)
  lock/
    local/                  In-process mutex-based Locker
    redis/                  Redis-backed distributed Locker (redsync)
  config/                   Secret key and runtime configuration
  chunker/                  CDC chunker (content-defined chunking algorithm)
  zstd/                     Pooled zstd reader/writer helpers
  analytics/                Analytics middleware types
db/
  migrations/{sqlite,postgres,mysql}/   bun/migrate SQL files (.up.sql / .down.sql pairs per engine)
  schema/{sqlite,postgres,mysql}.sql    Derived schema snapshots (via ncps migrate up)
```

**Removed from the repository:**
- `db/query.{sqlite,postgres,mysql}.sql` — sqlc query files
- `pkg/database/sqlitedb/` — sqlc-generated SQLite querier
- `pkg/database/postgresdb/` — sqlc-generated PostgreSQL querier
- `pkg/database/mysqldb/` — sqlc-generated MySQL/MariaDB querier
- `pkg/database/generated_*.go` — all generated wrapper/model/querier files
- `nix/dbmate-wrapper/` — external dbmate wrapper binary

#### Scenario: Package structure after migration
- **WHEN** the repository is inspected
- **THEN** `pkg/database/` contains only hand-written `.go` files with no sub-directories for engine-specific generated code

#### Scenario: No dbmate-wrapper directory
- **WHEN** the repository is inspected
- **THEN** `nix/dbmate-wrapper/` does not exist

---

### Requirement: Database Architecture
ncps uses `github.com/uptrace/bun` for all database access. `database.Open()` returns `*bun.DB` with the correct dialect applied for the detected engine. All callers accept `*bun.DB` directly — no `Querier` interface wraps it.

Query logic is written directly using the Bun query builder or `db.NewRaw(…)` for complex engine-specific SQL. All three engines are supported from a single code path; dialect differences are handled within `pkg/database/` using Bun's built-in dialect awareness.

**dbmate** and `nix/dbmate-wrapper/` are removed. Schema migrations are managed by `bun/migrate` with SQL files embedded at build time. The `ncps migrate` command is the sole interface for running migrations.

#### Scenario: Single code path for all engines
- **WHEN** a query is executed
- **THEN** the same Go code path handles SQLite, PostgreSQL, and MySQL; no engine-specific function dispatch occurs outside `pkg/database/`

#### Scenario: No Querier interface
- **WHEN** the codebase is inspected
- **THEN** no `database.Querier` interface exists; all callers hold `*bun.DB`

#### Scenario: Migrations via ncps binary
- **WHEN** an operator needs to apply schema migrations
- **THEN** they run `ncps migrate up`; no external binary is required

## REMOVED Requirements

### Requirement: sqlc-generated engine sub-packages
**Reason**: Replaced by a single Bun-based implementation in `pkg/database/`. Three separate generated packages created maintenance overhead and required `sqlc generate` + `go generate` after every query change.
**Migration**: Delete `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/`, and all `pkg/database/generated_*.go` files. Write queries using the Bun query builder in `pkg/database/` directly.

### Requirement: dbmate wrapper binary
**Reason**: Replaced by `bun/migrate` with embedded SQL files. The wrapper added nix packaging complexity and required an external binary at runtime.
**Migration**: Delete `nix/dbmate-wrapper/`. Remove dbmate from `nix/packages/`, dev shell, and all nix process-compose `preCheck` phases. Use `ncps migrate up` instead.
