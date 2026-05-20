// A4 bad fixture: edge.To with no reciprocal edge.From on the target.
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
		edge.To("orphans", Target.Type),
	}
}

// Target intentionally has no Edges() method — the edge.From back-reference
// is missing, so Ent will fabricate a phantom FK column on Target.
