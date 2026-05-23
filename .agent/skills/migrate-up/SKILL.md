---
description: Apply database migrations via `ncps migrate up`
---

# Apply Migrations

The `ncps` binary embeds the per-dialect Goose-formatted Atlas
migrations under `migrations/<dialect>/`. `ncps migrate up` selects the
appropriate dialect sub-FS based on the URL scheme and applies any
pending migrations via `goose.NewProvider`.

## Workflow

1. **Ensure the target database is reachable.** For local development,
   `nix run .#deps` starts ephemeral PostgreSQL, MySQL/MariaDB, MinIO,
   and Redis instances; SQLite needs no setup.

1. **Preview pending migrations (optional but recommended).** The
   `--dry-run` flag prints the detected state, the adoption action (if
   any), and the count of applied vs pending migrations without
   touching the database.

   ```bash
   ncps migrate up --dry-run --cache-database-url=sqlite:/path/to/db.sqlite
   ncps migrate up --dry-run --cache-database-url=postgresql://user:pass@host:port/db
   ncps migrate up --dry-run --cache-database-url=mysql://user:pass@host:port/db
   ```

1. **Apply the migrations.** Drop `--dry-run` to run them for real.

   ```bash
   ncps migrate up --cache-database-url=sqlite:/path/to/db.sqlite
   ncps migrate up --cache-database-url=postgresql://user:pass@host:port/db
   ncps migrate up --cache-database-url=mysql://user:pass@host:port/db
   ```

## Adoption (one-shot)

For deployments that previously used the dbmate-managed migrations, the
first `ncps migrate up` after upgrading performs a one-shot adoption:

- It inspects the existing `schema_migrations` table.
- If the shape is the legacy dbmate one, it converts the tracking table
  to the goose shape inside a single transaction (SQLite + Postgres) or
  via the RENAME/CREATE/copy/DROP backup-table dance (MySQL — restart-safe).
- All previously applied dbmate versions are recorded as goose-applied so
  the new migrator picks up only the truly pending ones.
- It then continues with the normal goose apply path.

Adoption is idempotent: re-running after a successful upgrade is a
no-op. **Back up your database before the first run** — the adoption is
forward-only and rollback requires a restore.

## Errors

- `ErrCorruptState`: the database has app tables but no
  `schema_migrations` table. This usually means a previous run crashed
  partway through `Schema.Create`. The safe response is to inspect the
  database and either drop the app tables (if the install is brand new)
  or restore from backup.
- `ErrImpossibleState` (MySQL only): the backup table is in a shape that
  cannot be reached by either S3 (happy path), S4 (resume from CREATE),
  or S5 (verify + drop). Manual inspection is required.
- `ErrDownNotSupported`: emitted by `ncps migrate down`. Migrations are
  forward-only — see `.agent/skills/migrate-down/SKILL.md` for the
  expand-contract recipe.
