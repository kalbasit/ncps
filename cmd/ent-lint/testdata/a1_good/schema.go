// A1 good fixture: CHECKs declared on the table-level Annotations() are OK.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

type WithTableCheck struct {
	ent.Schema
}

func (WithTableCheck) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{
			Checks: map[string]string{
				"with_table_check_counter_nonneg": "counter >= 0",
			},
		},
	}
}

func (WithTableCheck) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("counter"),
	}
}
