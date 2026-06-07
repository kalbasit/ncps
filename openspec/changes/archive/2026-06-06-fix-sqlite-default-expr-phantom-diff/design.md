## Context

GitHub issue #1328 reports that `task migrations:gen` rebuilds the unrelated `narinfos` table (a destructive create/copy/drop/rename swap) on **every** new SQLite migration. The issue hypothesized a CHECK-constraint normalization mismatch and suggested a one-time corrective migration.

Direct investigation (replaying the committed SQLite migrations into an in-memory DB and intercepting Atlas's computed changes via Ent's `schema.WithDiffHook`) disproved that hypothesis and pinpointed the real cause:

```
ModifyTable narinfos:  ModifyColumn last_accessed_at  (ChangeKind=64 = ChangeDefault)
ModifyTable nar_files: ModifyColumn last_accessed_at  (ChangeKind=64 = ChangeDefault)
  current  (inspected from stored DDL): RawExpr{"CURRENT_TIMESTAMP"}
  desired  (Ent):                       RawExpr{"(CURRENT_TIMESTAMP)"}
```

`last_accessed_at` (on `narinfos` and `nar_files` — the only two tables that have it, hence the only two rebuilt) declares its default via `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}`. Ent emits this as `schema.Expr("CURRENT_TIMESTAMP")` (Atlas `RawExpr`). Atlas's SQLite planner parenthesizes a `RawExpr` default → desired `(CURRENT_TIMESTAMP)`; Atlas's SQLite inspector strips the parens on readback → current `CURRENT_TIMESTAMP`. They never compare equal, so the diff is non-empty forever, and SQLite expresses a column-default change as a full table rebuild.

`created_at` (and `updated_at`) escape this because the `Timestamps` mixin declares the default via `entsql.Default("CURRENT_TIMESTAMP")`, emitted as the plain string `"CURRENT_TIMESTAMP"`, which Atlas treats as a recognized literal/function default and round-trips exactly. Postgres and MySQL are unaffected because their inspectors normalize the parenthesization.

Constraints: migrations are forward-only and existing files are immutable (`.claude/rules/ent-migrations.md`). The fix must not require editing or adding migration files, and must not change runtime default behavior.

## Goals / Non-Goals

**Goals:**
- Make SQLite `migrations:gen` produce an empty diff when no Ent field changed, and a table-scoped diff otherwise.
- Eliminate the need to hand-trim SQLite migrations for `narinfos`/`nar_files`.
- Prevent regression with a static lint guard.

**Non-Goals:**
- No corrective/data migration; no edits to existing migration files.
- No change to Postgres/MySQL output (already clean).
- No change to the runtime DB default of `last_accessed_at` (stays `CURRENT_TIMESTAMP`).
- No broad sweep of unrelated `DefaultExpr` usages.

## Decisions

### Decision 1: Fix at the Ent schema layer, not via a migration

Change `last_accessed_at` in `ent/schema/narinfo.go` and `ent/schema/nar_file.go` from `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` to `entsql.Default("CURRENT_TIMESTAMP")`, then regenerate the Ent client.

- **Why:** This makes Ent's *desired* schema use the same string-default representation that Atlas's SQLite inspector reports for the *current* schema, so the diff is empty. It mirrors the already-correct `created_at` in the `Timestamps` mixin.
- **Key property:** the emitted on-disk DDL is byte-identical (`DEFAULT (CURRENT_TIMESTAMP)`). Verified by regenerating and confirming `migrations:gen` produces no new SQLite file. Because the DDL is unchanged, **existing databases need no migration**.
- **Alternative — corrective migration (the issue's suggestion):** rejected. The stored DDL is already Atlas-canonical; rebuilding the table to the same DDL would not change what the inspector reads, so the phantom diff would persist. It also violates the forward-only/no-rebuild policy for no benefit.
- **Alternative — set `DefaultExpr: "(CURRENT_TIMESTAMP)"` (pre-parenthesized):** rejected as fragile — it depends on Atlas's internal paren handling and risks diverging Postgres/MySQL output.
- **Alternative — a diff-hook normalizer in `cmd/generate-migrations`** that drops paren-only `ChangeDefault` deltas: rejected as a heavier, less-obvious fix that masks the diff rather than making desired == current; the schema-layer fix is one line per table and self-documenting.

### Decision 2: Add a static lint guard (A6) in `cmd/ent-lint`

Add an AST check that fails on `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` and recommends `entsql.Default("CURRENT_TIMESTAMP")`.

- **Why:** the issue explicitly flags regression risk; this is the same class of guard `cmd/ent-lint` already provides for invariants A1/A2/A4, wired into `nix flake check`.
- **Scope:** narrowly targets the `CURRENT_TIMESTAMP` literal in a `DefaultExpr` annotation — the proven phantom-diff trigger — not all `DefaultExpr` usages.

## Risks / Trade-offs

- [Regenerated `ent/` client churn could hide an unintended change] → Confirm `git diff` on `ent/migrate/schema.go` shows only `last_accessed_at` flipping from `schema.Expr("CURRENT_TIMESTAMP")` to `"CURRENT_TIMESTAMP"`; run `task ent:check`.
- [Postgres/MySQL output might shift] → Run `task migrations:gen` for all three dialects (deps running) and confirm no new files are produced for any dialect.
- [`entsql.Default` semantics differ for time fields] → Mitigated by precedent: `created_at` already uses this exact form across all three dialects with clean diffs.
- [A6 lint false positives] → The check matches only the literal `CURRENT_TIMESTAMP` string inside a `DefaultExpr` annotation key, so other `DefaultExpr` uses are unaffected.

## Migration Plan

1. Edit the two schema files; `task ent:generate`.
2. `task ent:check` (lint + drift) and `task migrations:gen NAME=verify_no_phantom_diff` across all dialects — expect **no** new migration files; delete the probe if any tooling created one and re-confirm clean.
3. Implement and wire the A6 lint check; confirm it fails on the old form and passes on the new.
4. `task fmt && task lint && task test`.

Rollback: revert the schema annotation and regenerated client. No database state changes, so rollback is a pure code revert with no data implications.

## Open Questions

- None. Root cause, fix, and no-migration property are empirically confirmed.
