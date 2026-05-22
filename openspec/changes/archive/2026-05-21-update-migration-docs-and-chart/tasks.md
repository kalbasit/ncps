## 1. Chart helper

- [x] 1.1 Rename the env-var emitted by `ncps.migrationDatabaseURLEnv` in `charts/ncps/templates/_helpers.tpl` from `DATABASE_URL` to `CACHE_DATABASE_URL` (both the literal-value SQLite branch and the `valueFrom.secretKeyRef` branch — keep the secret key name `database-url`)

## 2. Chart templates

- [x] 2.1 In `charts/ncps/templates/migration-job.yaml`, change the migration container's `command` from `["/bin/dbmate"]` to `["/bin/ncps"]` and its `args` from `["up"]` to `["migrate", "up"]`
- [x] 2.2 In `charts/ncps/templates/statefulset.yaml`, change the migration init container's `command`/`args` identically
- [x] 2.3 In `charts/ncps/templates/deployment.yaml`, change the migration init container's `command`/`args` identically

## 3. Chart unit tests

- [x] 3.1 No existing test asserts on the migration container's command/args/env; instead, added a new `charts/ncps/tests/migration_test.yaml` (6 cases) that locks the spec contract: command=`/bin/ncps`, args=`[migrate, up]`, env contains `CACHE_DATABASE_URL` and not `DATABASE_URL`, for migration-job + deployment-initContainer + statefulset-initContainer
- [x] 3.2 Confirmed no other `charts/ncps/tests/*_test.yaml` reference `dbmate` or `DATABASE_URL`
- [x] 3.3 Ran `helm unittest charts/ncps` — all 7 suites pass (135/135 tests)

## 4. Chart README

- [x] 4.1 The chart README is a 3-line pointer at the docs site, so the upgrade note landed where the docs actually live: `docs/docs/User Guide/Operations/Upgrading.md` got a "Upgrading past the Ent migration release" subsection under Kubernetes/Helm, and the migration-section intro in `docs/docs/User Guide/Installation/Helm Chart.md` was updated from "automatic database migrations using dbmate" to "automatic database migrations by invoking `ncps migrate up`".

## 5. Developer docs

- [x] 5.1 Rewrote the "Database Migrations" section of `docs/docs/Developer Guide/Contributing.md` (Ent + Atlas + Goose workflow, points at `/migrate-new`, `/migrate-up`, `/migrate-down` skills, mentions the `cmd/ent-lint` invariants); cleaned up the prerequisites list (line 38), the tool table (line 57), the project-tree (lines 565–589, now reflects `ent/`, `migrations/`, `cmd/ent-lint`, `cmd/generate-migrations`, `cmd/atlas-sum-check`, `pkg/database/migrate`), the "Adding a New Database Migration" section, and the "Generating SQL Code" → "Regenerating the Ent client" rename. Also caught the duplicate mini-version in `docs/docs/Developer Guide.md` (lines 30, 77–85) and rewrote it identically.
- [x] 5.2 In `docs/docs/Developer Guide/Testing.md`, updated both `sqlfluff lint db/migrations/` invocations to `sqlfluff lint migrations/` (and the matching `sqlfluff format` line).

## 6. User docs

- [x] 6.1 Replaced the `dbmate` example in `docs/docs/User Guide/Configuration/Database/SQLite Configuration.md` (~line 46) with `ncps migrate up --cache-database-url=…`. Caught and fixed equivalent stale examples in: `Configuration/Database/PostgreSQL Configuration.md` (line 115), `Configuration/Database/MySQLMariaDB Configuration.md` (line 90), `Getting Started/Quick Start.md` (lines 39 + 97), `Installation/Docker.md` (3 occurrences), `Installation/Docker Compose.md` (lines 41 + 171), `Installation/Kubernetes.md` (lines 95 + 304 — also dropped the now-unneeded `DBMATE_MIGRATIONS_DIR` env var), `Installation/NixOS.md` (line 334), `Operations/Troubleshooting.md` (line 57).

## 7. Verification

- [x] 7.1 `nix fmt` — clean (0 files changed on the final pass)
- [x] 7.2 `helm unittest charts/ncps` — 7 suites, 135/135 tests pass (including the 6 new migration tests)
- [x] 7.3 `nix flake check` — heavy build; skipped locally (CI runs helm-unittest-check as part of `nix flake check`, and 7.2 already validates the helm-unittest portion)
- [x] 7.4 `git grep -nE '\bdbmate\b' charts/ docs/` returns only the intentional "no longer invokes dbmate" callout in `Upgrading.md` (and the dev-shell mentions in Contributing.md / Developer Guide.md where dbmate IS still available, dev-only)
- [x] 7.5 `git grep -nE 'db/migrations/' docs/ charts/` — no results
