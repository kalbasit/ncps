## Why

The dbmate→Ent migration removed `dbmate` from the runtime docker image
(only the `ncps` binary ships now), but three of the Helm chart's
templates and the `_helpers.tpl` partial still invoke `/bin/dbmate` and
set `DATABASE_URL`. The runtime image has no `/bin/dbmate`, so Helm
deploys break at the migration init container / job. The same drift
shows up in developer + user documentation, which still describes the
old `dbmate --migrations-dir db/migrations/<dialect>` workflow even
though the migrations now live at `migrations/<dialect>/` and are
applied by `ncps migrate up`.

## What Changes

- **BREAKING (for Helm consumers):** `charts/ncps/templates/migration-job.yaml`, `statefulset.yaml`, and `deployment.yaml` SHALL invoke `/bin/ncps migrate up` (command `/bin/ncps`, args `migrate up`) instead of `/bin/dbmate up`.
- The `ncps.migrationDatabaseURLEnv` helper in `_helpers.tpl` SHALL emit `CACHE_DATABASE_URL` (the env source already declared by the `ncps migrate up` CLI flag in `pkg/ncps/migrate.go`) instead of `DATABASE_URL`.
- The matching Helm unit tests under `charts/ncps/tests/` SHALL be updated so their command/env assertions reflect the new contract.
- `docs/docs/Developer Guide/Contributing.md` SHALL replace the dbmate-era workflow section (~`db/migrations/<dialect>/` paths, the dbmate-wrapper description, `dbmate new`/`dbmate up` examples) with the Ent + Atlas + Goose workflow already in use (`go generate ./ent/...`, `task migrations:gen NAME=…`, `ncps migrate up`).
- `docs/docs/Developer Guide/Testing.md` SHALL update the two `sqlfluff lint db/migrations/` references to point at `migrations/`.
- `docs/docs/User Guide/Configuration/Database/SQLite Configuration.md` SHALL replace its `dbmate --url=sqlite:… migrate up` example with the equivalent `ncps migrate up --cache-database-url=sqlite:…` (or `CACHE_DATABASE_URL=…  ncps migrate up`).

## Capabilities

### New Capabilities

- _(none)_

### Modified Capabilities

- `database-migrations`: clarify that the runtime migration entrypoint is `ncps migrate up` (binary `/bin/ncps`) reading the database URL from the `--cache-database-url` flag or the `CACHE_DATABASE_URL` env var — explicitly removing the requirement / implication that a `dbmate` binary needs to be present in the runtime image. The on-disk migration layout requirement (`migrations/<dialect>/`) is already in the spec; this change makes the entrypoint contract explicit so the Helm chart and docs have a single source of truth to point at.

## Impact

- **Code**: `charts/ncps/templates/{migration-job,statefulset,deployment}.yaml`, `charts/ncps/templates/_helpers.tpl`, `charts/ncps/tests/deployment_test.yaml` (and any other unit tests asserting the migration command).
- **Docs**: three files under `docs/docs/` (Contributing, Testing, SQLite Configuration). No structural changes to the doc tree; these are content swaps.
- **Helm consumers**: anyone deploying this chart from a build that includes the runtime-image dbmate removal must take this chart update together (otherwise migration containers `CrashLoopBackOff` with `exec: "/bin/dbmate": stat /bin/dbmate: no such file or directory`).
- **Performance / I/O / latency / memory**: no measurable change — same migration runner, different binary name.
- **No-impact areas**: the runtime serving path, the `pkg/database/migrate/` adoption logic, and the migration files themselves are untouched.

## Non-goals

- Re-adding `dbmate` to the runtime image. dbmate intentionally lives in the dev shell only (so `dev-scripts/run.py`'s `perform_clean()` can `dbmate drop` between scenarios).
- Changing the migration content / behavior. This change only updates the *invocation surface* (Helm + docs).
- Touching the e2e migration test (`dev-scripts/test-migration-e2e.py`) — it already exercises `ncps migrate up` via `run.py`.
- Adding a new capability or spec; the change refines wording inside the existing `database-migrations` spec.
