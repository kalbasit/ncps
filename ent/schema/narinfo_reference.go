package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NarInfoReference stores one row per (narinfo, reference) pair — the
// `References` field of the narinfo expanded across the relation. Primary
// key is composite on (narinfo_id, reference); there is no surrogate id.
type NarInfoReference struct {
	ent.Schema
}

// Annotations pins the on-disk table name.
//
// Note: ncps's dbmate-era schema had this table with composite PK
// (narinfo_id, reference) and no surrogate `id` column. Ent's codegen
// does not robustly support PK-less entities (see entc/gen "model"
// template issue with composite-PK entities), so this schema instead
// adopts a surrogate `id` PK plus a UNIQUE index on (narinfo_id,
// reference) which preserves the uniqueness invariant.
func (NarInfoReference) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "narinfo_references"},
	}
}

// Fields of the NarInfoReference.
func (NarInfoReference) Fields() []ent.Field {
	return []ent.Field{
		field.Int("narinfo_id"),
		field.String("reference").NotEmpty(),
	}
}

// Edges of the NarInfoReference.
func (NarInfoReference) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("narinfo", NarInfo.Type).
			Field("narinfo_id").
			Ref("references").
			Unique().
			Required(),
	}
}

// Indexes of the NarInfoReference.
func (NarInfoReference) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("narinfo_id", "reference").Unique(),
		index.Fields("reference"),
	}
}
