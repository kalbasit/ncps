## 1. Add Bun Dependencies

- [x] 1.1 Add `github.com/uptrace/bun`, `bun/dialect/sqlitedialect`, `bun/dialect/pgdialect`, `bun/dialect/mysqldialect`, and `bun/migrate` to `go.mod` / `go.sum`
- [x] 1.2 Verify `go mod tidy` leaves no stale entries and the project builds (go.mod/go.sum updated with bun v1.2.18; go mod tidy succeeds; full build blocked by task 4/7 type conflicts which are expected at this stage)

## 2. Write Integration Tests for All Existing Querier Methods

- [x] 2.1 Run `eval "$(enable-integration-tests)"` and `go test -race ./pkg/database/...` to establish the current passing baseline across SQLite, PostgreSQL, and MySQL
- [x] 2.2 Audit `pkg/database/contract_test.go` and `backends_test.go` — list every `Querier` method that has no direct test
- [x] 2.3 Add integration tests for any untested `Querier` methods (one sub-test per method, all three engines)
- [x] 2.4 Confirm full test suite passes on all three engines before touching any production code

## 3. Rename Migration Files to bun/migrate Convention

- [x] 3.1 For each engine (`sqlite`, `postgres`, `mysql`): split every existing dbmate migration file (`*.sql` with `-- migrate:up` / `-- migrate:down` markers) into `<timestamp>_<name>.up.sql` and `<timestamp>_<name>.down.sql`
- [x] 3.2 Delete the original single-file migrations after splitting
- [x] 3.3 Verify file naming: all files match `YYYYMMDDHHmmss_<name>.(up|down).sql` (31 split files created: 15 sqlite, 8 postgres, 8 mysql; originals deleted from db/migrations/; new files in pkg/database/migrations/)

## 4. Write New `pkg/database` Layer with Bun

- [x] 4.1 Create `pkg/database/models.go` with hand-written model structs (`NarInfo`, `NarFile`, `Chunk`, `Config`, `PinnedClosure`, and junction structs) — every field annotated with explicit `bun` tags; nullable fields use `sql.NullString` and `schema.NullTime`
- [x] 4.2 Update `pkg/database/database.go`: change `Open()` to return `*bun.DB`; wrap `*sql.DB` with `otelsql` before passing to `bun.NewDB`; apply correct dialect per engine
- [x] 4.3 Implement all query operations (previously in `generated_wrapper_*.go`) directly in `pkg/database/` using the Bun query builder or `db.NewRaw`; group by domain (`config.go`, `narinfo.go`, `nar_file.go`, `chunks.go`, `pinned_closures.go`)
- [x] 4.4 Implement transaction helpers using `db.RunInTx` / `bun.IDB`
- [x] 4.5 Add `go:embed` declarations and embed migration FS for all three engines in `pkg/database/migrations.go`
- [x] 4.6 Expose `Migrations(db *bun.DB) *migrate.Migrator` helper that returns a configured `bun/migrate` migrator for the given `*bun.DB`

## 5. Add `ncps migrate` Command

- [x] 5.1 Create `pkg/ncps/migrate.go` with the `migrate` CLI command and sub-commands: `up`, `up-to`, `down`, `down-to`, `status`
- [x] 5.2 Wire `migrate` command into `cmd/` entrypoint alongside `serve`
- [x] 5.3 Write unit tests for the `migrate` command (mock migrator or in-memory SQLite)

## 6. Compatibility Migration for Existing dbmate Databases

- [x] 6.1 Add the first bun migration (lowest timestamp) that detects and handles an existing `schema_migrations` table from dbmate so already-applied migrations are not re-run

## 7. Update All Callers from `Querier` to `*bun.DB`

- [x] 7.1 Search the entire codebase for all usages of `database.Querier` and update them to `*bun.DB`
- [x] 7.2 Update `pkg/cache/` to pass and store `*bun.DB` instead of `database.Querier`
- [x] 7.3 Update any test helpers or mocks that construct or accept `database.Querier`
- [x] 7.4 Delete `pkg/database/generated_querier.go`, `generated_wrapper_*.go`, `generated_errors.go`, `generated_models.go`
- [x] 7.5 Delete `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/` sub-packages
- [x] 7.6 Confirm `go build ./...` succeeds with no remaining references to old generated types

## 8. Remove sqlc Tooling

- [x] 8.1 Delete `sqlc.yaml` from the repo root
- [x] 8.2 Delete `db/query.sqlite.sql`, `db/query.postgres.sql`, `db/query.mysql.sql`
- [x] 8.3 Remove `sqlc` and `sqlc-multi-db` from `go.mod` / `go.sum` and from the Nix dev shell / packages
- [x] 8.4 Remove the `//go:generate go tool sqlc-multi-db …` directive from `pkg/database/database.go`

## 9. Remove dbmate and dbmate-wrapper

- [x] 9.1 Delete `nix/dbmate-wrapper/` directory entirely
- [x] 9.2 Remove `dbmate` and `dbmate.real` from `nix/packages/`, dev shell `nativeBuildInputs`, and Docker images
- [x] 9.3 Remove dbmate invocations from `nix/packages/ncps/default.nix` `preCheck` / `postCheck` phases
- [x] 9.4 Remove dbmate from `nix/process-compose/` and any other Nix modules that reference it
- [x] 9.5 Search the entire repo for remaining `dbmate` strings and remove them

## 10. Update Tests

- [x] 10.1 Run the full integration test suite (`eval "$(enable-integration-tests)" && go test -race ./...`) and fix any failures
- [ ] 10.2 Confirm all three engines pass: SQLite, PostgreSQL, MySQL

## 11. Update Skills and Documentation

- [x] 11.1 Update or remove `.agent/skills/dbmate/SKILL.md` (skill now covers creating plain `.up.sql` / `.down.sql` pairs)
- [x] 11.2 Update `.agent/skills/sqlc/SKILL.md` → replace with a `bun` skill documenting how to add/modify queries using the Bun query builder
- [x] 11.3 Delete `.agent/skills/generate-db-wrappers/SKILL.md` (no code generation step remains)
- [x] 11.4 Update `.agent/skills/migrate-new/SKILL.md`, `.agent/skills/migrate-up/SKILL.md`, `.agent/skills/migrate-down/SKILL.md` for `ncps migrate` workflow
- [x] 11.5 Update `CLAUDE.md`: replace all dbmate and sqlc workflow sections with `ncps migrate` and Bun query builder instructions
- [x] 11.6 Update `docs/` pages that mention dbmate, sqlc, or `go generate`

## 12. Lint and Format

- [x] 12.1 Run `golangci-lint run --fix ./...` and resolve any remaining lint errors
- [x] 12.2 Run `nix fmt` to format all project files
- [ ] 12.3 Run `go test -race ./...` (with integration tests enabled) for a final full-suite green run
