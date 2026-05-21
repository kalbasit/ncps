## Why

The current `sqlc + sqlc-multi-db + dbmate` pipeline forces three parallel hand-maintained SQL query files (one per engine), three generated wrappers, a separate migration runner, and lockstep edits across six files per schema change. Dialect divergence is only caught at test time. Adopting Ent for schema-as-code with Atlas-generated versioned migrations collapses this to one Go schema per table, auto-derived per-dialect DDL, and an embedded migration runner.

## What Changes

- **BREAKING**: Replace `sqlc-multi-db` codegen with `entgo.io/ent`. `pkg/database/` reorganises around the generated Ent client(s).
- **BREAKING**: Replace `dbmate` and `nix/dbmate-wrapper/` with an embedded `goose.Provider` driven by Atlas-generated `.sql` migrations.
- **BREAKING**: Replace `db/query.{sqlite,postgres,mysql}.sql` with Go schemas under `ent/schema/`. Call sites switch to the Ent fluent API.
- Replace `db/migrations/{sqlite,postgres,mysql}/` with `migrations/<dialect>/YYYYMMDDHHMMSS_<name>.sql` plus per-dialect `atlas.sum` integrity files.
- Add `cmd/ent-lint`: an AST + generated-SQL linter enforcing the five Ent codegen invariants (table-level CHECKs only; `OnDelete` only on `edge.To`; no field-level `Unique()` on edge-bound FK columns; reciprocal `edge.From().Ref()` for every `edge.To`; every `*_ciphertext` field carries `.Sensitive()`) plus the snake_case enum-type naming convention and the **expand-contract** safety rule (forbidden DDL — `DROP COLUMN`, `DROP TABLE`, `RENAME`, adding `NOT NULL` to a populated column — in the newest migration file fails the lint). Wired into `nix flake check`.
- Add `cmd/generate-migrations`: a Go program that imports `ariga.io/atlas` as a library and diffs the current Ent schema against the latest applied migration to emit a new versioned `.sql` file per dialect under `migrations/<dialect>/`. Supports a SQL-only mode that emits empty Goose-formatted stubs (for data backfills and constraint lock-ins) without an Ent diff.
- Introduce `go-task` for developer workflows. Add a top-level `Taskfile.dist.yml` defining at minimum:
  - `ent:generate` — runs `go generate ./ent/...` (gated by `sources:` checksum cache on `ent/schema/*.go`).
  - `ent:lint` — runs `go run ./cmd/ent-lint --root .`.
  - `ent:check` — depends on `ent:generate`, then runs `git diff --exit-code ./ent/` + `ent:lint`.
  - `migrations:gen NAME=<descriptive_name>` — depends on `ent:generate`, then runs `go run ./cmd/generate-migrations --name {{.NAME}}`.
  - `migrations:sql NAME=<descriptive_name>` — runs `go run ./cmd/generate-migrations --sql-only --name {{.NAME}}` to produce empty Goose-formatted stubs (data backfills, constraint lock-ins).
  A descriptive `NAME` is required by all migration tasks; placeholder names are rejected.
- Replace the `/migrate-new`, `/migrate-up`, `/migrate-down` skills:
  - "new" (schema-driven): edit `ent/schema/*.go`, then `task migrations:gen NAME=<descriptive_name>`.
  - "new" (SQL-only, e.g. data backfills): `task migrations:sql NAME=<descriptive_name>`.
  - "up" is `ncps migrate up` (embedded goose against the active DB URL).
  - "down" is unsupported — forward-only via the expand-contract recipe and the four-step NOT NULL promotion procedure.
- Keep all three engines: SQLite, PostgreSQL, MySQL/MariaDB.
- Update dev shell, Docker images, and Nix packaging: add `go-task` to the dev shell; drop `dbmate`, `dbmate-wrapper`, `sqlc`, `sqlc-multi-db`. Atlas is consumed as a Go library (`ariga.io/atlas`) by `cmd/generate-migrations`, not as an external CLI binary — nothing else new needs to be packaged. Pin `entgo.io/ent/cmd/ent` as a `go tool` directive so `go generate ./ent/...` works in any dev environment without bespoke installation. `go-task` is dev-only; runtime Docker images do not include it.

## Capabilities

### New Capabilities

- `database-orm`: Ent schemas under `ent/schema/*.go` as the single source of truth for tables, columns, indexes, edges, mixins, and CHECK annotations. The generated Ent client is committed.
- `database-migrations`: Atlas-generated, versioned, forward-only `.sql` migrations per dialect, embedded via `go:embed`, applied at runtime by `goose.Provider`. Schema-driven migrations come from `atlas migrate diff`; SQL-only migrations (backfills, constraint lock-ins) use a stub generator. Codifies the expand-contract policy, the four-step NOT NULL promotion recipe, and the descriptive-`NAME` rule.
- `ent-schema-lint`: Custom Go linter enforcing the five Ent codegen invariants, the snake_case enum-type naming convention, and the expand-contract DDL ban over schema AST and the generated SQL baselines.

### Modified Capabilities

- `data-model`: Same tables, columns, indexes, and constraints — but the source-of-truth representation moves from hand-written SQL to Ent schemas. Spec language referencing `db/query.*.sql` re-targets `ent/schema/`.

## Non-goals

- No schema-level change to existing tables, columns, indexes, or constraints. Behaviour parity only.
- No drop of MySQL/MariaDB support.
- No new ORM abstractions exposed above `pkg/database/`; the public surface is reshaped but the cache/server layers see equivalent CRUD primitives.
- No multi-DB or sharding; ncps remains a single-DB application.
- No live row migration tooling beyond Atlas/Goose. Reconciling existing `dbmate`-applied databases with the new migration baseline is in scope for `design.md`; moving production rows is not.
- CDC chunking, narinfo migration, fsck, and other cache-layer features stay untouched.

## Impact

- **Code**: `pkg/database/` rewritten around the Ent client; `generated_*` files removed. Callers in `pkg/cache/`, `pkg/ncps/`, `pkg/server/`, and `cmd/` switch to Ent's fluent API.
- **Migrations**: `db/migrations/*/`, `db/query.*.sql`, and `db/schema/` deleted; replaced by `migrations/<dialect>/` with `atlas.sum`.
- **Build & tooling**: `go.mod` adds `entgo.io/ent`, `ariga.io/atlas`, `github.com/pressly/goose/v3` as direct requires, and pins `entgo.io/ent/cmd/ent` in the `tool ()` directive. Drops `sqlc`, `sqlc-multi-db`. Dev shell and Docker images drop `dbmate` and its wrapper; no new external binaries are introduced (Atlas is library-only).
- **CI**: `nix flake check` gains an Ent-codegen drift check and the `ent-lint` pass. Migration tests continue to run against all three engines via existing process-compose dependencies.
- **Skills**: `.agent/skills/{migrate-new,migrate-up,migrate-down,sqlc,generate-db-wrappers}` are rewritten or removed.
- **Performance**: Hot streaming paths (NAR / NAR.zst) are unchanged. The ORM adds a small per-query constant over sqlc-generated code; no expected change to I/O, network latency, or memory footprint outside the Ent client's per-connection driver state.
- **Operations**: Operators using `dbmate` directly must adopt `ncps migrate up`. Reconciling pre-existing dbmate schema state with the new initial Atlas baseline is resolved in `design.md`.
