// A2 good fixture: OnDelete lives on edge.To, not edge.From.
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

func (Parent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("children", Child.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (Child) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("parent", Parent.Type).
			Ref("children").
			Unique().
			Required(),
	}
}
