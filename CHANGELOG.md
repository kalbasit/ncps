# Changelog

All notable changes to ncps are recorded in this file. The format roughly
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project loosely follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Database tooling migrated from sqlc + dbmate to Ent + Atlas + Goose.**
  Schemas are now authored under `ent/schema/*.go`, migrations are
  generated from Atlas diffs (used as a Go library) via
  `task migrations:gen NAME=<descriptive_snake_case>` (which regenerates
  the Ent client via its dependency on `ent:generate`, then calls
  `go run ./cmd/generate-migrations --name=...`), and applied at runtime
  by `ncps migrate up`. The runtime applier is `goose.NewProvider`
  against the embedded `migrations/<dialect>/` FS.

  See `CLAUDE.md` for the full developer workflow and the
  expand-contract policy + four-step NOT NULL recipe.

### Removed

- The `dbmate` and `dbmate-wrapper` binaries are no longer shipped in
  the dev shell or in Docker images.
- The `sqlc` codegen tooling and the generated `pkg/database/*db/`
  wrapper packages have been removed; callers now use the Ent client
  directly via `*database.Client`.

### Migration (operators)

**If you are upgrading an existing dbmate-managed deployment, BACK UP
YOUR DATABASE first.** The migration is forward-only and rollback
requires a restore.

The first `ncps migrate up` after upgrading performs a one-shot
adoption:

1. The new migrator inspects the existing `schema_migrations` table.
1. If the shape is the legacy dbmate one, it converts the tracking
   table to the goose shape:
   - On SQLite and PostgreSQL — inside a single transaction
     (`BEGIN; CREATE TEMP …; DROP TABLE schema_migrations; CREATE TABLE schema_migrations (goose shape); INSERT sentinel + preserved versions; verify row-count consistency (including the sentinel row); COMMIT;`).
   - On MySQL — via a RENAME → CREATE → sentinel → copy → verify →
     DROP backup-table dance that is safe to interrupt and resume.
1. All previously applied dbmate versions are recorded as
   goose-applied, so the new migrator picks up only the truly pending
   migrations.
1. The normal goose apply path then runs.

Adoption is idempotent — re-running after success is a no-op.

Operators with very large databases should run
`ncps migrate up --dry-run` first to preview the detected state and
adoption action without touching the database.

### CI

- New `nix flake check` derivations:
  - `ent-codegen-drift-check` — regenerates `ent/` and fails on diff.
  - `ent-lint-check` — runs `cmd/ent-lint --root .`; fails on any
    `[FAIL]` line.
  - `atlas-sum-check` — verifies every `migrations/<dialect>/atlas.sum`
    matches the directory contents.
  - `schema-equivalence-check` — runs the `TestSchemaEquivalence`
    golden test across SQLite, PostgreSQL, and MySQL.
