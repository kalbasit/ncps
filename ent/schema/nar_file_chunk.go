package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NarFileChunk is the ordered-sequence relation between nar_files and
// chunks for CDC-chunked NARs. ncps's dbmate-era schema used composite PK
// (nar_file_id, chunk_index); this schema uses a surrogate `id` PK plus a
// UNIQUE index on (nar_file_id, chunk_index) for the same business
// invariant — see NarInfoReference for the rationale.
type NarFileChunk struct {
	ent.Schema
}

// Annotations pins the on-disk table name.
func (NarFileChunk) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "nar_file_chunks"},
	}
}

// Fields of the NarFileChunk.
func (NarFileChunk) Fields() []ent.Field {
	return []ent.Field{
		field.Int("nar_file_id"),
		field.Int("chunk_id"),
		field.Int("chunk_index"),
	}
}

// Edges of the NarFileChunk.
func (NarFileChunk) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("nar_file", NarFile.Type).
			Field("nar_file_id").
			Ref("chunk_links").
			Unique().
			Required(),
		edge.From("chunk", Chunk.Type).
			Field("chunk_id").
			Ref("nar_file_links").
			Unique().
			Required(),
	}
}

// Indexes of the NarFileChunk.
func (NarFileChunk) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("nar_file_id", "chunk_index").Unique(),
		index.Fields("chunk_id"),
	}
}
