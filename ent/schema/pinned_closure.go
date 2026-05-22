package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/kalbasit/ncps/internal/entmixin"
)

// PinnedClosure holds one row per pinned narinfo hash. A pinned closure is
// exempt from LRU eviction.
type PinnedClosure struct {
	ent.Schema
}

// Annotations declares the on-disk table name.
func (PinnedClosure) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "pinned_closures"},
	}
}

// Mixin of PinnedClosure.
func (PinnedClosure) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the PinnedClosure.
func (PinnedClosure) Fields() []ent.Field {
	return []ent.Field{
		field.String("hash").NotEmpty(),
	}
}

// Indexes of the PinnedClosure.
func (PinnedClosure) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("hash").Unique(),
	}
}
