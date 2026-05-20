package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NarInfoSignature stores one row per (narinfo, signature) pair — the
// `Sig` lines of the narinfo expanded across the relation. Primary key is
// composite on (narinfo_id, signature).
type NarInfoSignature struct {
	ent.Schema
}

// Annotations pins the on-disk table name. See NarInfoReference for why we
// use a surrogate `id` PK plus a composite UNIQUE index rather than a
// composite PK.
func (NarInfoSignature) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "narinfo_signatures"},
	}
}

// Fields of the NarInfoSignature.
func (NarInfoSignature) Fields() []ent.Field {
	return []ent.Field{
		field.Int("narinfo_id"),
		field.String("signature").NotEmpty(),
	}
}

// Edges of the NarInfoSignature.
func (NarInfoSignature) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("narinfo", NarInfo.Type).
			Field("narinfo_id").
			Ref("signatures").
			Unique().
			Required(),
	}
}

// Indexes of the NarInfoSignature.
func (NarInfoSignature) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("narinfo_id", "signature").Unique(),
		index.Fields("signature"),
	}
}
