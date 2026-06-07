// A6 good fixture: a CURRENT_TIMESTAMP default declared via
// entsql.Default(...) is OK — Ent emits a string default that Atlas's
// SQLite inspector round-trips exactly (issue #1328).
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
)

type WithEntsqlDefault struct {
	ent.Schema
}

func (WithEntsqlDefault) Fields() []ent.Field {
	return []ent.Field{
		field.Time("last_accessed_at").
			Optional().
			Nillable().
			Default(time.Now).
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
	}
}
