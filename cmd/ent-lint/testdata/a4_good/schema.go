// A4 good fixture: edge.To has a reciprocal edge.From().Ref() on the target.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
)

type Source struct {
	ent.Schema
}

type Target struct {
	ent.Schema
}

func (Source) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("targets", Target.Type),
	}
}

func (Target) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("source", Source.Type).
			Ref("targets").
			Unique().
			Required(),
	}
}
