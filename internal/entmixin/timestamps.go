// Package entmixin provides reusable Ent schema mixins for the ncps Ent
// schemas under ent/schema/.
package entmixin

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// Timestamps contributes the project-standard `created_at` and `updated_at`
// columns to any Ent schema that embeds it via `Mixin()`.
//
//   - `created_at` is NOT NULL with DB-level DEFAULT CURRENT_TIMESTAMP, set
//     on insert and never modified afterwards (immutable).
//   - `updated_at` is nullable; the application sets it explicitly when a
//     row is modified.
//
// The DB-level DEFAULT on `created_at` is declared via `entsql.Default`
// so that raw-SQL inserts (notably from migration backfills and test
// helpers) succeed without specifying the column.
type Timestamps struct {
	mixin.Schema
}

// Fields of the Timestamps mixin.
func (Timestamps) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
		field.Time("updated_at").
			Optional().
			Nillable(),
	}
}

// Annotations is required for the mixin interface; no schema-wide
// annotations are needed here because the per-field annotations above are
// what the dialect codegen reads.
func (Timestamps) Annotations() []schema.Annotation {
	return nil
}
