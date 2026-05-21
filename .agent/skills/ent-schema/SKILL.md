---
description: Edit Ent schemas safely — the five codegen invariants
---

# Editing Ent Schemas

The Ent schemas under `ent/schema/*.go` are the single source of truth
for the database DDL. Atlas diffs the regenerated client (under `ent/`)
against the on-disk migration history to emit new SQL migrations. The
following invariants are enforced by `cmd/ent-lint` and verified in CI
via the `ent-lint-check` derivation; A3 and A5 are tracked in the
ent-schema-lint spec and currently enforced by code review.

## The Five Invariants

### A1 — No field-level `entsql.Check(...)`

Ent **silently drops** field-level `entsql.Check(...)` annotations.
CHECK constraints MUST live on table-level `Annotations()`:

```go
// WRONG — silently dropped.
field.Int("size").Annotations(entsql.Check("size >= 0"))

// RIGHT — CHECK lives on the schema's Annotations().
func (Chunk) Annotations() []schema.Annotation {
    return []schema.Annotation{
        entsql.Checks(map[string]string{
            "chunks_size_nonneg": "size >= 0",
        }),
    }
}
```

### A2 — `entsql.OnDelete(...)` lives on `edge.To`, never on `edge.From`

Ent silently ignores `entsql.OnDelete(...)` annotations attached to
`edge.From(...)` declarations. CASCADE / RESTRICT / SET NULL semantics
MUST be declared on the owning `edge.To(...)` side, otherwise the
generated DDL falls back to the default ON DELETE rule and the intended
behaviour disappears without warning.

```go
// Correct — OnDelete on edge.To (the FK owner).
func (Parent) Edges() []ent.Edge {
    return []ent.Edge{
        edge.To("children", Child.Type).
            Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
    }
}

// Wrong — OnDelete on edge.From is silently dropped.
func (Child) Edges() []ent.Edge {
    return []ent.Edge{
        edge.From("parent", Parent.Type).Ref("children").Unique().
            Annotations(entsql.Annotation{OnDelete: entsql.Cascade}),
    }
}
```

### A3 — UNIQUE + edge.From().Field() pairing

A field bound by `edge.From(...).Ref(...).Field(...)` is the foreign-key
column on the child side. If that field is also `UNIQUE`, both
declarations MUST emit duplicate-index annotations — otherwise Ent
fabricates a phantom index. The lint cross-references field-level
`.Unique()` calls against `edge.From().Field(...)` declarations across
all schemas.

### A4 — Every `edge.To` has a reciprocal `edge.From().Ref()`

Ent silently fabricates a phantom FK column on the target schema if an
`edge.To(...)` declaration is missing its reciprocal `edge.From(...).Ref(...)`
on the target schema. The lint walks every edge declaration across the
whole schema tree and reports mismatches.

```go
// On Parent.
func (Parent) Edges() []ent.Edge {
    return []ent.Edge{
        edge.To("children", Child.Type),
    }
}

// On Child — required reciprocal.
func (Child) Edges() []ent.Edge {
    return []ent.Edge{
        edge.From("parent", Parent.Type).Ref("children").Unique(),
    }
}
```

### A5 — `*_ciphertext` fields are `.Sensitive()`

Every `field.Bytes("*_ciphertext")` field MUST chain `.Sensitive()` so
the ciphertext never leaks into error messages, log lines, or the
generated `String()` methods.

### Snake_case enum-type naming (convention; not yet linted)

Ent uses snake_case enum-type names by convention. Every `field.Enum(...)`
needs a matching `entsql.Annotation{Type: "<table>_<column>_enum"}` so
the generated DDL emits a stable, predictable type name across dialects.
This is enforced by code review until ncps's schema tree gains its first
`field.Enum(...)` declaration and the check is wired into `cmd/ent-lint`.

```go
field.Enum("compression").
    Values("none", "xz", "zstd").
    Annotations(entsql.Annotation{Type: "nar_files_compression_enum"})
```

```go
field.Bytes("password_ciphertext").Sensitive()
```

## Workflow

1. Edit `ent/schema/<entity>.go`. New entities go in a new file.
1. Run `go generate ./ent/...` (or `task ent:generate`) to regenerate
   the `ent/` tree. Commit the regenerated tree alongside the schema
   change — `ent-codegen-drift-check` in CI rejects stale generated
   code.
1. Run `go run ./cmd/ent-lint --root .` (or `task ent:lint`) and verify
   no `[FAIL]` lines.
1. Run `task ent:check` to do both at once.

## Generating migrations after a schema change

`cmd/generate-migrations` diffs the regenerated client against the
existing migration history and emits one new `.sql` per dialect under
`migrations/<dialect>/`. See `.agent/skills/migrate-new/SKILL.md` for
the migration workflow.

## Enforcement details

`cmd/ent-lint/main.go` implements:

- A1 via `findFieldEntsqlCheck` (AST walk for `.Annotations(... entsql.Check(...) ...)`
  chained on a field builder).
- A2 (the `OnDelete` variant of "annotation goes on the wrong side";
  AST walk for `entsql.OnDelete(...)` on `edge.From(...)` chains).
- A4 via `checkA4` (cross-file walk for `edge.To` declarations without
  a reciprocal `edge.From().Ref()` on the target schema).

A3, A5, the snake_case enum-type check, the expand-contract DDL ban on
the newest migration files, and the CHECK presence cross-validation
against generated SQL are tracked in the ent-schema-lint spec and not
yet implemented in `cmd/ent-lint`.
