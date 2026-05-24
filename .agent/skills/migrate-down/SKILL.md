---
description: Roll back a migration — DON'T. Use the expand-contract policy.
---

# There Is No `migrate down`

`ncps migrate down` exits non-zero with `ErrDownNotSupported`. The
migration tree is **forward-only by design** — every `.sql` file under
`migrations/<dialect>/` is sealed by `atlas.sum`, the runtime only
applies (`goose.Up`), and no operator-facing rollback is provided.

This rules out the entire class of "fix it by reverting" patterns that
mid-deployment column rewrites depend on. To change schema safely, use
the **expand-contract policy** instead.

## Expand-contract policy

Column changes that aren't purely additive — type changes, NOT NULL
additions, renames — must be split across multiple deploys so old and
new binaries can coexist against the same schema.

1. **Add** the new column (nullable) in migration N.
1. **Backfill** the new column in migration N+1 (use `--sql-only` —
   `go run ./cmd/generate-migrations --sql-only --name=backfill_x`).
1. **Switch reads** to the new column in the application code; deploy.
1. **Remove** the old column in migration N+2. `DROP COLUMN` is flagged
   as sensitive DDL by the migration spec and is only permissible at
   this final step, once no deployed binary references the old column.

Each migration ships in its own release; the application gracefully
handles the dual-column state in between.

## Four-step NOT NULL recipe

The specific case of adding a NOT NULL constraint to an existing
nullable column:

1. **Migration A**: ADD COLUMN nullable, default-able.
1. **Deploy** application code that always writes a non-null value for
   the new column (so all rows written after this deploy have a value).
1. **Migration B** (`--sql-only` stub): BACKFILL existing null rows.
1. **Migration C**: ADD CONSTRAINT NOT NULL (or
   `ALTER COLUMN ... SET NOT NULL`).
1. **Deploy each step independently**; never combine into a single
   migration.

## What if I really really need to undo?

You don't. Migrations are sealed; the only path "backward" is forward —
a new migration that reverses the intent of the previous one. If the
schema is genuinely broken, restore from backup; never edit a committed
migration file or its `atlas.sum`.

See `CLAUDE.md` and `openspec/specs/data-model/spec.md` for the
rationale and additional context.
