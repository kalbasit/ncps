// A6 bad fixture: a CURRENT_TIMESTAMP default declared via
// entsql.Annotation{DefaultExpr: ...} is forbidden — Ent emits it as a
// parenthesized RawExpr that Atlas's SQLite inspector does not round-trip,
// producing a perpetual phantom table rebuild (issue #1328).
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
)

type WithDefaultExpr struct {
	ent.Schema
}

func (WithDefaultExpr) Fields() []ent.Field {
	return []ent.Field{
		field.Time("last_accessed_at").
			Optional().
			Nillable().
			Default(time.Now).
			Annotations(entsql.Annotation{
				DefaultExpr: "CURRENT_TIMESTAMP",
			}),
	}
}
