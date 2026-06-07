// A6 bad fixture (pointer form): the forbidden DefaultExpr default is just
// as broken when passed as a pointer to the composite literal
// (&entsql.Annotation{...}) — entsql.Annotation has a value-receiver Name(),
// so the pointer form also satisfies schema.Annotation and compiles. The
// linter must unwrap the address-of operator and still flag it (issue #1328).
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
)

type WithDefaultExprPointer struct {
	ent.Schema
}

func (WithDefaultExprPointer) Fields() []ent.Field {
	return []ent.Field{
		field.Time("last_accessed_at").
			Optional().
			Nillable().
			Default(time.Now).
			Annotations(&entsql.Annotation{
				DefaultExpr: "CURRENT_TIMESTAMP",
			}),
	}
}
