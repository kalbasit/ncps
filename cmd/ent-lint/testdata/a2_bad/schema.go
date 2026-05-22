// A2 bad fixture: entsql.OnDelete on edge.From is forbidden.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
)

type Parent struct {
	ent.Schema
}

type Child struct {
	ent.Schema
}

func (Child) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("parent", Parent.Type).
			Ref("children").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
