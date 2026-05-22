package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/kalbasit/ncps/internal/entmixin"
)

// Chunk holds one row per unique chunk content hash. Chunks are
// zstd-compressed on disk; `compressed_size` tracks the on-disk byte count.
// The chunk hash is the Nix base32 representation of the chunk content,
// 52 characters.
type Chunk struct {
	ent.Schema
}

// Annotations declares table-level CHECK constraints.
//
// Per the data-model spec: size and compressed_size must be non-negative.
// CHECK constraints live exclusively in Annotations (invariant A1).
func (Chunk) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{
			Checks: map[string]string{
				"chunks_size_nonneg":            "size >= 0",
				"chunks_compressed_size_nonneg": "compressed_size >= 0",
			},
		},
	}
}

// Mixin of Chunk.
func (Chunk) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the Chunk.
func (Chunk) Fields() []ent.Field {
	return []ent.Field{
		field.String("hash").NotEmpty(),
		field.Uint32("size"),
		field.Uint32("compressed_size").Default(0),
	}
}

// Edges of the Chunk.
//
// The nar_file_chunks back-reference is intentionally exposed as a plain
// edge (not Through) because the join table's PK is on
// (nar_file_id, chunk_index), not on the FK columns — see NarFile.Edges.
func (Chunk) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("nar_file_links", NarFileChunk.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the Chunk.
func (Chunk) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("hash").Unique(),
	}
}
