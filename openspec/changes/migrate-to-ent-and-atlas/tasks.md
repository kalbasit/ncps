## 1. MySQL spike (decision gate)

- [x] 1.1 Add a throwaway branch with a single `ent/schema/spike.go` declaring one toy entity with fields, an index, an edge, and a CHECK annotation
- [x] 1.2 Write a one-shot Go program that calls `entgo.io/ent/dialect/sql/schema.NewMigrate` with `dialect.MySQL`, `sqltool.GooseFormatter`, and `sqltool.NewGooseDir` against an empty MariaDB 11 dev DB; capture the generated `.sql` file
- [x] 1.3 Apply the generated file with `goose.NewProvider(goose.DialectMySQL, db, dir, WithTableName("schema_migrations")).Up(ctx)` and confirm the table is created exactly as Ent's `migrate.Tables` expects
- [x] 1.4 Decision gate: if the pipeline works end-to-end, document the result in `design.md` and delete the spike branch; if it fails, write a ~50-LOC custom Goose formatter for MySQL and validate the same flow before proceeding

## 2. Foundation setup

- [ ] 2.1 Add `entgo.io/ent`, `ariga.io/atlas`, `github.com/pressly/goose/v3` as direct requires in `go.mod`
- [ ] 2.2 Pin `entgo.io/ent/cmd/ent` in the `tool ()` directive in `go.mod`
- [ ] 2.3 Add `go-task` to the Nix dev shell in `flake.nix` and confirm `task --version` resolves inside `nix develop`
- [ ] 2.4 Create `Taskfile.dist.yml` at the repo root with `ent:generate`, `ent:lint`, `ent:check`, `migrations:gen`, and `migrations:sql` tasks per the design's D8
- [ ] 2.5 Run `task --list` and verify the five expected tasks are discoverable
- [ ] 2.6 Add `entgo.io/ent` go-generate marker file at `ent/generate.go` containing `//go:generate go tool ent generate ./schema`

## 3. Schema-parity test baseline (TDD)

- [x] 3.1 Write `pkg/database/contract_test.go` additions that assert exact column lists, nullability, indexes, FKs, and CHECK presence for `config`, `narinfos`, `narinfo_references`, `narinfo_signatures`, `nar_files`, `narinfo_nar_files`, `chunks`, `nar_file_chunks`
- [x] 3.2 Confirm the new tests pass against the current dbmate-applied schema for SQLite, PostgreSQL, and MySQL (via process-compose deps)
- [x] 3.3 Commit these tests separately — they form the regression bar that every subsequent step must preserve

## 4. Ent schemas (one file per entity)

- [x] 4.1 Create `internal/entmixin/` with a `Timestamps` mixin contributing `created_at` and `updated_at` fields
- [x] 4.2 Author `ent/schema/config.go` matching the existing `config` table exactly (column types, nullability, defaults, UNIQUE index on `key`) — Go type renamed to `ConfigEntry` because `Config` collides with Ent's predeclared identifier; on-disk table pinned to "config" via `entsql.Annotation{Table: ...}`
- [x] 4.3 Author `ent/schema/narinfo.go` matching `narinfos` (including the denormalised columns, UNIQUE on `hash`, index on `last_accessed_at`, and the table-level CHECK on `file_size`/`nar_size`)
- [x] 4.4 Author `ent/schema/narinfo_reference.go` matching `narinfo_references` (surrogate `id` PK + composite UNIQUE index on `(narinfo_id, reference)` per design D10b, FK to `narinfos` with `ON DELETE CASCADE`, index on `reference`)
- [x] 4.5 Author `ent/schema/narinfo_signature.go` matching `narinfo_signatures`
- [x] 4.6 Author `ent/schema/nar_file.go` matching `nar_files` (CDC state columns, UNIQUE on `(hash, compression, query)`, `file_size` as `field.Uint64`)
- [x] 4.7 Author `ent/schema/narinfo_nar_file.go` matching `narinfo_nar_files` (surrogate `id` PK + composite UNIQUE index on `(narinfo_id, nar_file_id)` per design D10b, both FK cascades and both lookup indexes)
- [x] 4.8 Author `ent/schema/chunk.go` matching `chunks` (UNIQUE on `hash`, table-level CHECKs for `size >= 0` and `compressed_size >= 0`, `size`/`compressed_size` as `field.Uint32`)
- [x] 4.9 Author `ent/schema/nar_file_chunk.go` matching `nar_file_chunks` (surrogate `id` PK + composite UNIQUE index on `(nar_file_id, chunk_index)` per design D10b, both FK cascades, index on `chunk_id`)
- [x] 4.9b Author `ent/schema/pinned_closure.go` matching `pinned_closures` (UNIQUE on `hash`) — also extends the §3 schema-parity tests to cover this table
- [x] 4.10 Run `go generate ./ent/...` and commit the resulting `ent/` tree
- [x] 4.11 Run the schema-parity tests from §3 against a database created by applying Ent's `Schema.Create` and confirm zero divergence (temporary verification — `Schema.Create` is not the final apply path)

## 5. `cmd/ent-lint` (TDD: fixtures first)

- [x] 5.1 Create `cmd/ent-lint/testdata/` with positive and negative fixture directories for each of A1, A2, A4 (A3, A5, snake-case-enum, expand-contract, CHECK-presence fixtures tracked separately — see 5.5/5.7/5.8/5.9/5.10 below)
- [x] 5.2 Write `cmd/ent-lint/main_test.go` asserting one `[FAIL]` line per negative fixture and all `[PASS]` lines for the positive fixtures (covers A1, A2, A4 today)
- [x] 5.3 Implement A1: AST walk for field-level `entsql.Check(...)` annotations
- [x] 5.4 Implement A2: AST walk for `OnDelete` annotations on `edge.From()` chains
- [ ] 5.5 Implement A3: cross-file AST walk for `field.X().Unique()` columns that are also bound by `edge.From().Field(...)` elsewhere
- [x] 5.6 Implement A4: cross-file AST walk for `edge.To` declarations without a reciprocal `edge.From().Ref()` on the target schema
- [ ] 5.7 Implement A5: AST walk for `field.Bytes("*_ciphertext")` declarations without a chained `.Sensitive()`
- [ ] 5.8 Implement the snake_case enum-type check (A `field.Enum(...)` without `entsql.Annotation{Type: "<table>_<column>_enum"}`)
- [ ] 5.9 Implement the expand-contract check on the newest file in each `migrations/<dialect>/` directory
- [ ] 5.10 Implement the CHECK presence cross-validation against generated `.sql` baselines for every dialect
- [x] 5.11 Implement checklist-formatted output (`[PASS]` / `[FAIL]` + invariant id + message); exit non-zero on any `[FAIL]`
- [x] 5.12 Run `go test ./cmd/ent-lint` and confirm all fixtures pass
- [x] 5.13 Run `cmd/ent-lint --root .` against the current `ent/schema/` tree and confirm it exits zero

## 6. `cmd/generate-migrations` (TDD)

- [x] 6.1 Write `cmd/generate-migrations/main_test.go` with smoke tests: TestSQLOnlyEmitsThreeDialects (three .sql files with shared timestamp) + TestNameValidation (rejects placeholders, accepts descriptive names). Full schema-driven round-trip ("zero diff against current Ent + translated migrations") moves to §8 where it lives naturally next to the schema-equivalence assertion.
- [x] 6.2 Implement the binary in `cmd/generate-migrations/main.go`: flags `--name`, `--sql-only`, `--root`, `--postgres-url`, `--mysql-url` (with `NCPS_GEN_POSTGRES_URL` / `NCPS_GEN_MYSQL_URL` env fallbacks). One timestamp shared across the three dialect output files.
- [x] 6.3 Per-dialect logic: SQLite via in-memory `sqlite3`, Postgres via the dev URL, MySQL via the dev URL with `ParseTime=true` + `MultiStatements=true`
- [x] 6.4 Implement `--sql-only` mode that writes empty Goose stubs (`-- +goose Up\n\n-- +goose Down\n`) without touching Ent or any DB
- [x] 6.5 Implement the placeholder-name guard (reject `auto`, `wip`, `tmp`, `todo`, `temp`, `test`, empty, whitespace)
- [x] 6.6 Update `atlas.sum` per dialect after each generation — handled by `sqltool.NewGooseDir`'s built-in integrity writer; Atlas regenerates `atlas.sum` whenever `NamedDiff` writes a file
- [x] 6.7 Run `task migrations:gen NAME=spike_test` against the current Ent tree; verify three files appear under `migrations/<dialect>/`; revert the result — deferred to §8 where it lives naturally as part of the schema-equivalence golden test (running this here would write into the repo's `migrations/` tree)
- [x] 6.8 Run `task migrations:sql NAME=spike_backfill_test` and verify three empty stubs appear; revert the result — covered by TestSQLOnlyEmitsThreeDialects (uses --root in a temp dir, so no repo mutation)

## 7. Migration translation (1:1)

- [x] 7.1 For each existing `db/migrations/sqlite/*.sql`, copy to `migrations/sqlite/` preserving the timestamp and rewrite `-- migrate:up` → `-- +goose Up`, `-- migrate:down` → `-- +goose Down`
- [x] 7.2 Repeat for `db/migrations/postgres/*.sql` → `migrations/postgres/`
- [x] 7.3 Repeat for `db/migrations/mysql/*.sql` → `migrations/mysql/`
- [ ] 7.4 Generate `atlas.sum` for each dialect directory via the Atlas library call (run `cmd/generate-migrations --recompute-sums` or equivalent) — deferred to §6 (cmd/generate-migrations writes atlas.sum as part of its first invocation)
- [x] 7.5 Author `migrations/migrations.go` with `//go:embed sqlite/* postgres/* mysql/*` exposing a single `MigrationsFS embed.FS` var (named `FS` here for ergonomics — `migrations.FS` reads better than `migrations.MigrationsFS`)
- [x] 7.6 Verify `MigrationsFS` enumerates the expected file count per dialect (8 postgres, 8 mysql, 14 sqlite). Adds `TestEmbedFS` and `TestGooseApplySQLite` smoke tests; full multi-engine schema-equivalence is §8.

## 8. Schema-equivalence golden test + ent_baseline bridge migration

Per design D6 (Option E), the §7 translated migrations do **not** by themselves reach the Ent-expected schema — there is a structural difference (D10b surrogate `id` columns on weak entities) plus, on Postgres, the deliberate `BIGSERIAL → IDENTITY` modernization for v1. The bridge migration closes the gap; §8's golden test gates both the translation and the bridge.

- [x] 8.1 Author the SQLite ent_baseline bridge file under `migrations/sqlite/20260520212039_ent_baseline.sql` — generated by `go run ./cmd/generate-migrations --name=ent_baseline --skip=postgres`. Atlas's SQLite output recreates each affected table via the temp-table-copy-rename pattern (the canonical SQLite ALTER strategy).
- [x] 8.2 Author the MySQL ent_baseline bridge file under `migrations/mysql/20260520212039_ent_baseline.sql` — same generator invocation. Atlas emits `ALTER TABLE ... ADD COLUMN id BIGINT AUTO_INCREMENT, DROP PRIMARY KEY, ADD PRIMARY KEY (id), ADD UNIQUE INDEX ...` for the weak entities plus `MODIFY COLUMN ... varchar(255)` / `bigint` / `timestamp` cosmetic alignments and FK constraint renames.
- [x] 8.3 Author the Postgres bridge as TWO migrations (the BIGSERIAL/IDENTITY incompatibility means Atlas can't auto-compute the diff in one shot):
  - `migrations/postgres/20260520212038_postgres_serial_to_identity.sql` — hand-written. For each of the five existing `id`-bearing tables (`narinfos`, `config`, `nar_files`, `chunks`, `pinned_closures`): DROP DEFAULT on `id`, ADD GENERATED BY DEFAULT AS IDENTITY, setval the new IDENTITY's sequence to `MAX(id)`, DROP the orphaned `<table>_id_seq`.
  - `migrations/postgres/20260520213017_ent_baseline.sql` — generated by `go run ./cmd/generate-migrations --name=ent_baseline --skip=sqlite,mysql` after the hand-written file is in place. Atlas computes the remaining diff cleanly: surrogate `id` on weak entities, `text → varchar`, `integer → bigint` for chunks size/compressed_size, `timestamp → timestamptz`, FK constraint renames, CHECK constraint renames.
- [x] 8.4 `atlas.sum` per dialect generated automatically by `cmd/generate-migrations` thanks to the `ensureAtlasSum` helper added in this commit (extends §6's generator).
- [x] 8.5 `migrations/equivalence_test.go::TestSchemaEquivalence` added: per dialect, opens a fresh empty database, replays every `migrations/<dialect>/*.sql` via Atlas's `schema.ModeReplay` with the `GooseFormatter`, then calls `NamedDiff` against `migrate.Tables` into a temp directory. Asserts no new `.sql` file appears (zero diff). All three dialects pass.
- [ ] 8.6 `TestSchemaCreateMatchesAppliedMigrations` (Option E's fresh-install ↔ applied-migrations equivalence) is deferred to §9 where the fresh-install path (`Schema.Create` + version seeding) is implemented — testing that path requires the path to exist.
- [x] 8.7 Wire §8 tests into `nix flake check` — implemented in §13.4 via the `schema-equivalence-check` derivation.

## 9. `ncps migrate up` command + adoption (Option E)

Per design D6 (Option E), the adoption decision tree has four branches: empty DB → `Schema.Create` + version seeding; dbmate-shape → table conversion + goose handoff (which applies bridge or bridge+missing-dbmate-era files); goose-shape → goose handoff (normal incremental path); unexpected → diagnostic abort.

- [x] 9.1 Define the state-detection probe in `pkg/database/migrate/state.go`: introspects dialect-specific catalogs to determine empty / dbmate-shape / goose-shape / MySQL-mid-adoption-S4 / MySQL-mid-adoption-S5 / impossible-S6.
  - **Known edge case (deferred for follow-up)**: if `Schema.Create` succeeds but the process crashes before any version stamp is seeded into `schema_migrations`, the next `migrate up` sees app tables but no tracking table and currently returns `ErrCorruptState`. A future enhancement would add a `StatePartialFresh` state that resumes the version-seeding step idempotently — but care is needed because the same shape can arise from genuine corruption, manual operator actions, or leftover state from a different install, and auto-recovery would mask those. For v1 the safe default of refusing to adopt is preferred; operators can manually drop and re-run.
- [x] 9.2 State coverage gated by the integration test suite (each scenario in `migrate_test.go` exercises `Detect` indirectly via `Up`; per-fixture state tests rolled into 9.4–9.10).
- [x] 9.3 Implement the **empty DB fresh-install path**: `freshInstall` in `fresh.go` calls `entSchema.NewMigrate(drv).Create(ctx, migrate.Tables...)` then uses goose's own `database.NewStore` to create `schema_migrations` and insert the version-0 sentinel + every embedded version. (Schema.Create requires a process-wide mutex because Ent mutates `migrate.Tables`; harmless in production where only one process runs `migrate up`.)
- [x] 9.4 `migrate_test.go::TestMigrateUp/<dialect>/fresh_install` covers fresh-install across SQLite, Postgres, and MySQL.
- [x] 9.5 Implement **SQLite + Postgres transactional table adoption** in `adopt.go`: `BEGIN; CREATE TEMP …; DROP TABLE schema_migrations; CREATE TABLE schema_migrations (goose shape); INSERT sentinel + INSERT preserved versions; verify row-count parity; COMMIT;`
- [x] 9.6 `migrate_test.go::TestMigrateUp/<dialect>/dbmate_full_history_upgrade` and `/dbmate_partial_v04_era_upgrade` (SQLite-only) cover those transitions.
- [x] 9.7 Implement **MySQL backup-table adoption** S3 happy path: RENAME → CREATE → sentinel → copy → verify → DROP backup.
- [x] 9.8 Implement MySQL S4 (resume from CREATE) and S5 (verify + drop, or re-copy + drop on mismatch).
- [x] 9.9 Implement MySQL S6 diagnostic (`ErrImpossibleState`).
- [x] 9.10 `migrate_test.go::TestMigrateUpMySQLCrashRecovery` covers S4 / S5 / S6 with race-detector clean.
- [x] 9.11 Implement the `ncps migrate up` CLI subcommand wiring in `pkg/ncps/migrate.go`; registered in `pkg/ncps/root.go` Commands list.
- [x] 9.12 Implement `--dry-run` via `migrate.DryRun` returning a `Plan` struct; CLI prints state + adoption action + applied/pending counts without touching the database.
- [x] 9.13 `ncps migrate down` exits non-zero with `ErrDownNotSupported` pointing at the expand-contract recipe.
- [x] 9.14 End-to-end coverage: fresh install (all 3 dialects), dbmate full upgrade (all 3 dialects), v0.4-era partial upgrade (SQLite), re-run idempotency (all 3 dialects), MySQL crash recovery (S4/S5/S6) — 13 subtests total, all pass under `-race`.

## 10. `pkg/database/` rewrite

- [x] 10.1 Replace `database.Querier` return type with `*database.Client` (wraps `*ent.Client` + driver metadata) in `database.Open(url, poolCfg)` (introduced parallel surface in §10; signature swap deferred to §11 to keep build green)
- [x] 10.2 Implement `(*database.Client).WithTransaction(ctx, name, fn func(*ent.Tx) error) error` preserving the OTel span/error wrapping the legacy helper provided
- [x] 10.3 Keep `database.DetectFromDatabaseURL` and `database.PoolConfig` as-is; verify their tests still pass

## 11. Caller migration

- [x] 11.1 Rewrite `pkg/cache/cache.go` storage of `database.Querier` to `*database.Client`; convert call sites one method at a time (added `*Client` field + `dbClient` param threaded through `cache.New`, testhelpers, and all call sites; call-site method conversions in §11.2-§11.8)
- [x] 11.2 Convert `GetNarInfo*` paths in `pkg/cache/` to Ent fluent API; run package tests
- [x] 11.3 Convert `PutNarInfo*` paths; run package tests
- [x] 11.4 Convert `GetNarFile*` / `PutNarFile*` paths (including CDC chunk insertion); run package tests
- [x] 11.5 Convert chunk and orphan-cleanup queries; run package tests
- [x] 11.6 Convert `pkg/ncps/` paths (migration tooling, fsck, closure pinning); run package tests
- [x] 11.7 Convert `pkg/server/` paths; run package tests
- [x] 11.8 Convert `cmd/` paths; run integration tests
- [x] 11.9 Run `go test -race ./...` against all three engines and confirm parity with the pre-change baseline

## 12. Cleanup

- [x] 12.1 Delete `db/query.sqlite.sql`, `db/query.postgres.sql`, `db/query.mysql.sql`
- [x] 12.2 Delete `db/schema/sqlite.sql`, `db/schema/postgres.sql`, `db/schema/mysql.sql`
- [x] 12.3 Delete `db/migrations/sqlite/`, `db/migrations/postgres/`, `db/migrations/mysql/` (after confirming the translated files are committed under `migrations/<dialect>/`)
- [x] 12.4 Delete `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/`
- [x] 12.5 Delete `pkg/database/generated_models.go`, `pkg/database/generated_errors.go`, `pkg/database/generated_querier.go`, `pkg/database/generated_wrapper_{sqlite,postgres,mysql}.go`
- [x] 12.6 Delete `nix/dbmate-wrapper/` and remove `dbmate-wrapper` from any Nix package or dev-shell reference
- [x] 12.7 Remove `dbmate` from the dev shell and Docker images
- [x] 12.8 Remove `github.com/kalbasit/sqlc-multi-db` from the `tool ()` directive and from `go.mod`'s indirect requires (run `go mod tidy`)
- [x] 12.9 Delete `sqlc.yaml`

## 13. CI integration

- [x] 13.1 Add an `ent-codegen-drift-check` derivation in `nix/checks/` that runs `go generate ./ent/...` then `git diff --exit-code ./ent/`
- [x] 13.2 Add an `ent-lint-check` derivation that runs `cmd/ent-lint --root .` and asserts zero `[FAIL]` lines
- [x] 13.3 Add an `atlas-sum-check` derivation that verifies every `migrations/<dialect>/atlas.sum` matches the directory contents
- [x] 13.4 Add a `schema-equivalence-check` derivation that runs the §8 golden test for all three engines (uses process-compose deps)
- [x] 13.5 Verify `nix flake check` passes end-to-end with all four new derivations contributing
- [x] 13.6 Confirm the existing `.github/workflows/ci.yml` still passes (no edits expected — the new checks plug into `nix flake check`)

## 14. Docs and skills

- [x] 14.1 Update `CLAUDE.md`: replace the sqlc / dbmate sections with the Ent / Atlas / Goose workflow; document the expand-contract policy and four-step NOT NULL recipe
- [ ] 14.2 Rewrite `.agent/skills/migrate-new/SKILL.md` to drive the `task migrations:gen NAME=…` and `task migrations:sql NAME=…` workflows
- [ ] 14.3 Rewrite `.agent/skills/migrate-up/SKILL.md` to drive `ncps migrate up` (mentioning the `--dry-run` flag for upgrades)
- [ ] 14.4 Rewrite `.agent/skills/migrate-down/SKILL.md` to point at the expand-contract policy instead of describing a down command
- [ ] 14.5 Delete `.agent/skills/sqlc/` and `.agent/skills/generate-db-wrappers/`
- [ ] 14.6 Add a `.agent/skills/ent-schema/SKILL.md` documenting the five codegen invariants and the snake_case enum-type convention
- [ ] 14.7 Update the project `README.md` to mention Ent + Atlas + Goose under "Architecture" / "Development"
- [ ] 14.8 Add a `CHANGELOG.md` entry calling out the upgrade procedure for operators with existing dbmate-managed deployments (backup advised; first `migrate up` performs the one-shot adoption)
- [ ] 14.9 Run `nix fmt` and `golangci-lint run --fix` over the entire tree as a final pass

## 15. Standardize the `data-model` openspec spec

- [x] 15.1 Restructure `openspec/specs/data-model/spec.md` into the canonical openspec `### Requirement:` / `#### Scenario:` format, preserving every claim and constraint from the current freeform document (the Overview, Database Engines table, Toolchain Conventions, Schema, `Querier` Interface, and Entity Relationship Summary sections). Each existing paragraph or table entry that asserts a behaviour becomes a `### Requirement:` block with at least one `#### Scenario:` using WHEN/THEN.
- [x] 15.2 Confirm `openspec validate data-model` (or the project-equivalent validator) accepts the restructured spec.
- [x] 15.3 Rewrite this change's `openspec/changes/migrate-to-ent-and-atlas/specs/data-model/spec.md` delta to use `## MODIFIED Requirements` blocks targeting the now-existing requirements (e.g. "Requirement: The database engine is selected at runtime via URL scheme", "Requirement: sqlc generates per-engine Querier interfaces"). Replace the current ADDED + REMOVED structure with MODIFIED + REMOVED where the modification semantics fit; keep ADDED for genuinely new requirements (e.g. Ent-fluent-API call sites).
- [x] 15.4 Run `openspec status --change migrate-to-ent-and-atlas` and confirm the change still validates with the rewritten delta.
- [x] 15.5 Order: 15.1 and 15.2 SHOULD land before any of §10–§12 (the call-site rewrite and cleanup) so the spec model is coherent during the bulk implementation; 15.3 and 15.4 land alongside 15.1 in the same PR.
