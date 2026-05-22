// A1 bad fixture: field-level entsql.Check is forbidden.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
)

type WithFieldCheck struct {
	ent.Schema
}

func (WithFieldCheck) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("counter").
			Annotations(entsql.Check("counter >= 0")),
	}
}
