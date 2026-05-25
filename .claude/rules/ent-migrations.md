# Ent Schema Migrations Workflow

Whenever changes are made to the Ent schema (`apps/uar/ent/*/schema/*.go`), the
versioned migrations must be regenerated and the four codegen invariants below
MUST be honoured.

## Rule

If you add, modify, or remove fields in the Ent schema for the `control`,
`blobs`, or `catalog` databases, you MUST explicitly run the migration
generation task to create the new versioned `.sql` migrations for BOTH
supported dialects (`sqlite` and `postgres`).

You MUST provide a descriptive `NAME` for the migration. Do NOT use placeholder
names.

Do NOT simply modify the schema and assume the database will automatically diff
and migrate. The `Schema.Create` runtime method is no longer used for database
migrations.

## Codegen invariants

These five patterns are silently miscompiled by Ent or represent latent security
risks. They are enforced by `task uar:ent:lint` (which runs as part of
`task uar:ent:check`).

### 1. CHECK constraints MUST be table-level

Field-level `field.X(...).Annotations(entsql.Check(...))` is dropped by Ent's
codegen on both SQLite and Postgres. Declare CHECK constraints exclusively at
the table level via the schema's `Annotations()` method:

```go
func (Blob) Annotations() []schema.Annotation {
    return []schema.Annotation{
        entsql.Annotation{
            Checks: map[string]string{
                "blobs_ref_count_nonneg":    "ref_count >= 0",
                "blobs_file_hash_len_chk":   "length(file_hash) = 64",
            },
        },
    }
}
```

Use the dialect-keyed map form when SQL syntax differs between sqlite and
postgres.

### 2. `OnDelete` lives on `edge.To`, never on `edge.From`

Ent only honours `entsql.OnDelete(...)` annotations on the `edge.To` (parent)
side of a relationship. Annotations on `edge.From` are silently dropped. Always
declare the cascade rule on the parent schema.

### 3. Field-level `Unique()` is forbidden on edge-bound columns

If a column is the FK of an edge (i.e. some `edge.From(...).Field("col")` or
`edge.To(...).Field("col")` references it), do NOT also declare
`field.X("col").Unique()`. Combining them causes Ent's codegen to emit _neither_
a column-level UK nor an edge-derived UK. Uniqueness for one-to-one edges is
declared exclusively via the edge:
`edge.From(...).Field("col").Unique().Required()`.

### 4. Every `edge.To` requires a reciprocal `edge.From().Field()`

A one-sided `edge.To` causes Ent to fabricate a phantom FK column with an
auto-generated name on the target table. Every `edge.To(name, T)` MUST have a
matching `edge.From(_, A.Type).Field(_).Ref(name)` on the target schema. If a
relationship is not actually wanted, remove the `edge.To` rather than leaving
it without a back-edge.

### 5. Every `_ciphertext` field MUST carry `.Sensitive()`

Any `field.Bytes("*_ciphertext")` declaration MUST chain `.Sensitive()` so that
Ent's generated `String()` method emits `<sensitive>` instead of formatting the
raw ciphertext bytes. Without `Sensitive()`, any code path that logs or
`%v`-formats the entity struct leaks on-disk ciphertext into the log stream —
a violation of `.claude/rules/encrypted-columns.md`.

```go
field.Bytes("private_key_ciphertext").
    GoType(crypto.Ciphertext{}).
    Sensitive()
```

This is enforced at the source level (AST check on the schema file) by
`task uar:ent:lint`. The `Sensitive()` annotation is a schema-only metadata
change; it does NOT alter the SQL column definition and requires no migration.

## Snake_case enum types

When a `field.Enum(...)` field generates a Postgres ENUM type, give it an
explicit snake\*case name via `entsql.Annotation{Type: "<table>*<column>\_enum"}`
on the field. Without it, Ent emits PascalCase enum type names that do not
match the project's naming convention.

## Migration compatibility

**Production is live. The pre-production rebaseline allowance has permanently expired.** Never delete, edit, or replace any existing migration file. All schema changes MUST ship as new forward-only additive `.sql` migration files.

### Expand-contract rule

Every migration applied to a production database MUST be safe to run while the immediately preceding version of the application is still serving traffic. This means:

- **Allowed**: adding a nullable column, adding a new table, adding an index, adding a new ENUM value (Postgres), adding a NOT NULL column to a newly-created (empty) table.
- **Forbidden in a single migration**: `DROP COLUMN`, `DROP TABLE`, `RENAME COLUMN`, `RENAME TABLE`, adding `NOT NULL` to an existing nullable column that has live rows.

`task uar:ent:lint` enforces this by failing when the newest `.sql` migration file in any shard/dialect directory contains a forbidden DDL statement.

### Four-step NOT NULL promotion recipe

When you need to add a NOT NULL column to a table that has existing rows, split the work across multiple PRs/deployments:

1. **Step 1 — expand**: Add the column as `NULL DEFAULT NULL`. Use Ent schema with `Optional()` and generate via `task uar:migrations:gen NAME=add_<col>_nullable`. Safe to deploy immediately.
1. **Step 2 — application code**: Update writes so new rows always set `<col>`. Deploy this with Step 1 or separately.
1. **Step 3 — backfill**: Create a SQL-only migration to `UPDATE … SET <col> = <expr> WHERE <col> IS NULL`. Use `task uar:migrations:sql NAME=backfill_<col>` to generate the stub, fill in the SQL, then deploy.
1. **Step 4 — contract**: Remove `Optional()` from the Ent schema (so it becomes NOT NULL), then run `task uar:migrations:gen NAME=lock_<col>_not_null`. Deploy after Step 3 is fully applied and no NULLs remain.

## Example

After modifying a schema:

1. Run lint to confirm the five codegen invariants:
   `task uar:ent:lint`
1. Generate migrations for all shards and dialects:
   `task uar:migrations:gen NAME=<descriptive_name>`
   - This task automatically runs `ent:generate` before generating migrations.
1. Verify that new `.sql` files are created in
   `apps/uar/migrations/<shard>/<dialect>/`.
1. Run `task uar:ent:check` (which includes `task uar:ent:lint` and the diff
   guard) and confirm it exits zero.
1. Commit both the Ent schema changes and the generated `.sql` migration files.

To create a SQL-only migration file (e.g. a data backfill or constraint lock-in that is not driven by an Ent schema change):

`task uar:migrations:sql NAME=<descriptive_name>`

This generates empty Goose-formatted `.sql` stub files in all shard/dialect directories with the correct timestamp prefix. Fill in the SQL body before committing.
