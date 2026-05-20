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

- [ ] 8.1 Author the SQLite ent_baseline bridge file under `migrations/sqlite/<ts>_ent_baseline.sql` — run `task migrations:gen NAME=ent_baseline` against the current Ent schemas + §7 translated history; Atlas auto-generates the surrogate-`id` SQL via the temp-table-rename pattern SQLite requires.
- [ ] 8.2 Author the MySQL ent_baseline bridge file under `migrations/mysql/<ts>_ent_baseline.sql` — same `task migrations:gen` invocation produces the file (one shared timestamp across all three dialects).
- [ ] 8.3 Author the Postgres ent_baseline bridge file under `migrations/postgres/<ts>_ent_baseline.sql` — hand-written ~30 lines of DDL: `ALTER TABLE ... ALTER COLUMN id DROP DEFAULT; DROP SEQUENCE <table>_id_seq; ALTER TABLE ... ALTER COLUMN id ADD GENERATED BY DEFAULT AS IDENTITY` for each of the seven `id`-bearing tables; `ALTER TABLE ... ADD COLUMN id BIGSERIAL PRIMARY KEY; ALTER TABLE ... ADD CONSTRAINT ... UNIQUE (...)` for the four weak entities. Atlas cannot auto-generate this due to its BIGSERIAL/IDENTITY diff-engine limitation.
- [ ] 8.4 Update `atlas.sum` for each dialect directory (`cmd/generate-migrations` writes these automatically when it runs; for the hand-written Postgres bridge, add a separate atlas.sum regeneration step at the end).
- [ ] 8.5 Add `migrations/equivalence_test.go::TestSchemaEquivalence` that, per dialect, opens a fresh database, replays every `migrations/<dialect>/*.sql` via Atlas's `schema.ModeReplay`, then calls `NamedDiff` against `migrate.Tables` into a temp directory. Assert no new `.sql` file appears (zero diff).
- [ ] 8.6 Add `migrations/equivalence_test.go::TestSchemaCreateMatchesAppliedMigrations` that, per dialect: opens DB-A → applies all migrations via goose; opens DB-B → calls `entSchema.NewMigrate(drv).Create(ctx, migrate.Tables...)`. Introspect both schemas (column lists, constraints, indexes via dialect-specific catalog queries) and assert byte-equivalence. This is the load-bearing guarantee for Option E's fresh-install path.
- [ ] 8.7 Wire §8 tests into `nix flake check` so any future schema/migration drift fails CI.

## 9. `ncps migrate up` command + adoption (Option E)

Per design D6 (Option E), the adoption decision tree has four branches: empty DB → `Schema.Create` + version seeding; dbmate-shape → table conversion + goose handoff (which applies bridge or bridge+missing-dbmate-era files); goose-shape → goose handoff (normal incremental path); unexpected → diagnostic abort.

- [ ] 9.1 Define the state-detection probe in `pkg/database/migrate/state.go`: introspect dialect-specific catalogs to determine empty / dbmate-shape / goose-shape / MySQL-mid-adoption-S4 / MySQL-mid-adoption-S5 / impossible-S6
- [ ] 9.2 Write `pkg/database/migrate/state_test.go` covering every state per dialect against fixtures
- [ ] 9.3 Implement the **empty DB fresh-install path**: detect empty, call `entSchema.NewMigrate(drv).Create(ctx, migrate.Tables...)`, then walk `migrations.FS` for the dialect, parse each filename's leading 14-char timestamp, and insert one row per version into `schema_migrations` with `is_applied=true` and `tstamp=NOW()`. The seeding step SHALL be atomic per dialect (transaction on sqlite/postgres; single multi-row INSERT for MySQL).
- [ ] 9.4 Write integration tests for the fresh-install path: open empty DB per dialect, run `migrate up`, assert (a) schema matches `Schema.Create` output, (b) `schema_migrations` contains exactly the version stamps from `migrations/<dialect>/`, all with `is_applied=true`, (c) a subsequent `migrate up` is a no-op (no DDL issued)
- [ ] 9.5 Implement **SQLite + Postgres transactional table adoption**: `BEGIN; CREATE TEMP TABLE … AS SELECT …; DROP TABLE schema_migrations; CREATE TABLE schema_migrations (…goose schema…); INSERT …; <verify>; COMMIT;`
- [ ] 9.6 Write integration tests for SQLite + Postgres adoption from partial (3, 7, full) dbmate states, plus a rollback-on-verify-failure test
- [ ] 9.7 Implement **MySQL backup-table adoption** per S3 happy path: RENAME → CREATE → INSERT → verify → DROP backup
- [ ] 9.8 Implement MySQL recovery for S4 (resume from CREATE) and S5 (verify row-count, drop or re-insert+drop)
- [ ] 9.9 Implement MySQL S6 diagnostic (impossible state; abort with operator-readable message)
- [ ] 9.10 Write MySQL state-machine integration tests covering S3→S4, S3→S5-equal, S3→S5-unequal crash recovery; assert each ends in the canonical adopted state
- [ ] 9.11 Implement the `ncps migrate up` cli subcommand wiring: open DB, run adoption probe, dispatch to the appropriate path (fresh / dbmate-shape / goose-shape), then hand to `goose.NewProvider(dialect, db, fs.Sub(MigrationsFS, dialect), WithTableName("schema_migrations")).Up(ctx)` for the dbmate-shape and goose-shape paths
- [ ] 9.12 Implement `--dry-run`: prints detected state, would-be action (fresh / adopt-dbmate / nothing-to-do), list of pending versions; issues no DDL and modifies no rows
- [ ] 9.13 Implement `ncps migrate down` as an explicit non-zero exit with the expand-contract / four-step NOT NULL pointer
- [ ] 9.14 End-to-end test matrix per engine: (a) fresh install lands on Ent state with all versions seeded; (b) v0.4-era partial dbmate state upgrades to Ent state via dbmate-era files + bridge; (c) dbmate end-state install upgrades via bridge only; (d) post-upgrade install with one extra incremental migration applies only the new version. All four scenarios must terminate with the schema-parity (§3) and schema-equivalence (§8) tests passing.

## 10. `pkg/database/` rewrite

- [ ] 10.1 Replace `database.Querier` return type with `*database.Client` (wraps `*ent.Client` + driver metadata) in `database.Open(url, poolCfg)`
- [ ] 10.2 Implement `(*database.Client).WithTransaction(ctx, name, fn func(*ent.Tx) error) error` preserving the OTel span/error wrapping the legacy helper provided
- [ ] 10.3 Keep `database.DetectFromDatabaseURL` and `database.PoolConfig` as-is; verify their tests still pass

## 11. Caller migration

- [ ] 11.1 Rewrite `pkg/cache/cache.go` storage of `database.Querier` to `*database.Client`; convert call sites one method at a time
- [ ] 11.2 Convert `GetNarInfo*` paths in `pkg/cache/` to Ent fluent API; run package tests
- [ ] 11.3 Convert `PutNarInfo*` paths; run package tests
- [ ] 11.4 Convert `GetNarFile*` / `PutNarFile*` paths (including CDC chunk insertion); run package tests
- [ ] 11.5 Convert chunk and orphan-cleanup queries; run package tests
- [ ] 11.6 Convert `pkg/ncps/` paths (migration tooling, fsck, closure pinning); run package tests
- [ ] 11.7 Convert `pkg/server/` paths; run package tests
- [ ] 11.8 Convert `cmd/` paths; run integration tests
- [ ] 11.9 Run `go test -race ./...` against all three engines and confirm parity with the pre-change baseline

## 12. Cleanup

- [ ] 12.1 Delete `db/query.sqlite.sql`, `db/query.postgres.sql`, `db/query.mysql.sql`
- [ ] 12.2 Delete `db/schema/sqlite.sql`, `db/schema/postgres.sql`, `db/schema/mysql.sql`
- [ ] 12.3 Delete `db/migrations/sqlite/`, `db/migrations/postgres/`, `db/migrations/mysql/` (after confirming the translated files are committed under `migrations/<dialect>/`)
- [ ] 12.4 Delete `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/`
- [ ] 12.5 Delete `pkg/database/generated_models.go`, `pkg/database/generated_errors.go`, `pkg/database/generated_querier.go`, `pkg/database/generated_wrapper_{sqlite,postgres,mysql}.go`
- [ ] 12.6 Delete `nix/dbmate-wrapper/` and remove `dbmate-wrapper` from any Nix package or dev-shell reference
- [ ] 12.7 Remove `dbmate` from the dev shell and Docker images
- [ ] 12.8 Remove `github.com/kalbasit/sqlc-multi-db` from the `tool ()` directive and from `go.mod`'s indirect requires (run `go mod tidy`)
- [ ] 12.9 Delete `sqlc.yaml`

## 13. CI integration

- [ ] 13.1 Add an `ent-codegen-drift-check` derivation in `nix/checks/` that runs `go generate ./ent/...` then `git diff --exit-code ./ent/`
- [ ] 13.2 Add an `ent-lint-check` derivation that runs `cmd/ent-lint --root .` and asserts zero `[FAIL]` lines
- [ ] 13.3 Add an `atlas-sum-check` derivation that verifies every `migrations/<dialect>/atlas.sum` matches the directory contents
- [ ] 13.4 Add a `schema-equivalence-check` derivation that runs the §8 golden test for all three engines (uses process-compose deps)
- [ ] 13.5 Verify `nix flake check` passes end-to-end with all four new derivations contributing
- [ ] 13.6 Confirm the existing `.github/workflows/ci.yml` still passes (no edits expected — the new checks plug into `nix flake check`)

## 14. Docs and skills

- [ ] 14.1 Update `CLAUDE.md`: replace the sqlc / dbmate sections with the Ent / Atlas / Goose workflow; document the expand-contract policy and four-step NOT NULL recipe
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
