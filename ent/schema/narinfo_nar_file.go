package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NarInfoNarFile is the M:N join table between narinfos and nar_files.
// Multiple narinfos can reference the same physical NAR file, and one
// narinfo MAY reference multiple nar_files when stored in different
// (compression, query) variants.
type NarInfoNarFile struct {
	ent.Schema
}

// Annotations pins the on-disk table name. See NarInfoReference for why
// the schema uses a surrogate `id` PK plus a composite UNIQUE index.
func (NarInfoNarFile) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "narinfo_nar_files"},
	}
}

// Fields of the NarInfoNarFile.
func (NarInfoNarFile) Fields() []ent.Field {
	return []ent.Field{
		field.Int("narinfo_id"),
		field.Int("nar_file_id"),
	}
}

// Edges of the NarInfoNarFile.
func (NarInfoNarFile) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("narinfo", NarInfo.Type).
			Field("narinfo_id").
			Ref("nar_info_nar_files").
			Unique().
			Required(),
		edge.From("nar_file", NarFile.Type).
			Field("nar_file_id").
			Ref("nar_info_nar_files").
			Unique().
			Required(),
	}
}

// Indexes of the NarInfoNarFile mirror the dbmate-era secondary indexes so
// lookups by either side of the join continue to use an index, plus a
// composite UNIQUE preserves the original PK uniqueness invariant.
func (NarInfoNarFile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("narinfo_id", "nar_file_id").Unique(),
		index.Fields("narinfo_id"),
		index.Fields("nar_file_id"),
	}
}
