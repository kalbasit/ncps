package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/kalbasit/ncps/internal/entmixin"
)

// NarInfo holds one row per cached .narinfo. Narinfo fields are stored
// denormalised inline so reads are a single-row lookup. The `url`,
// `compression`, and related fields MAY be NULL while the narinfo exists in
// the database as a stub before its full metadata is populated.
type NarInfo struct {
	ent.Schema
}

// Annotations pins the on-disk table name to "narinfos" (overriding
// Ent's default "nar_infos" pluralisation) and declares table-level CHECK
// constraints.
//
// Per the data-model spec: file_size and nar_size must be non-negative.
// Field-level entsql.Check annotations are silently dropped by Ent's
// codegen (invariant A1), so CHECKs live exclusively here.
func (NarInfo) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{
			Table: "narinfos",
			Checks: map[string]string{
				"narinfos_file_size_nonneg": "file_size >= 0",
				"narinfos_nar_size_nonneg":  "nar_size >= 0",
			},
		},
	}
}

// Mixin of NarInfo.
func (NarInfo) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the NarInfo.
func (NarInfo) Fields() []ent.Field {
	return []ent.Field{
		field.String("hash").NotEmpty(),
		field.String("store_path").Optional().Nillable(),
		field.String("url").Optional().Nillable(),
		field.String("compression").Optional().Nillable(),
		field.String("file_hash").Optional().Nillable(),
		field.Int64("file_size").Optional().Nillable(),
		field.String("nar_hash").Optional().Nillable(),
		field.Int64("nar_size").Optional().Nillable(),
		field.String("deriver").Optional().Nillable(),
		field.String("system").Optional().Nillable(),
		field.String("ca").Optional().Nillable(),
		field.Time("last_accessed_at").
			Optional().
			Nillable().
			Default(time.Now),
	}
}

// Edges of the NarInfo.
//
// The narinfo<->nar_file M:N is modelled as a plain edge to the
// NarInfoNarFile join entity (rather than Ent's Through pattern) because
// the join entity uses a surrogate `id` PK with a composite UNIQUE index,
// not a composite PK on the FK columns.
func (NarInfo) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("references", NarInfoReference.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("signatures", NarInfoSignature.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("nar_info_nar_files", NarInfoNarFile.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the NarInfo.
func (NarInfo) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("hash").Unique(),
		index.Fields("last_accessed_at"),
	}
}
