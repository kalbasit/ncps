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

// NarFile holds one row per unique (hash, compression, query) combination.
// It tracks both whole-file NARs and CDC-chunked NARs; the CDC state lives
// in `total_chunks` + `chunking_started_at` (see data-model spec for the
// state machine).
type NarFile struct {
	ent.Schema
}

// Annotations declares the on-disk table name (singular Ent default would
// otherwise be `nar_files` automatically, but we name it explicitly for
// robustness against Ent's pluralisation rules changing).
func (NarFile) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "nar_files"},
	}
}

// Mixin of NarFile.
func (NarFile) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the NarFile.
func (NarFile) Fields() []ent.Field {
	return []ent.Field{
		field.String("hash").NotEmpty(),
		field.String("compression").
			Default(""),
		field.Uint64("file_size"),
		field.String("query").
			Default("").
			StorageKey("query"),
		field.Int64("total_chunks").
			Default(0),
		field.Time("chunking_started_at").
			Optional().
			Nillable(),
		field.Time("verified_at").
			Optional().
			Nillable(),
		// bytes_stored_at records when PutNar durably wrote the NAR's bytes. It is
		// distinct from verified_at (fsck's integrity-check timestamp): a narinfo-PUT
		// placeholder leaves it NULL, so it proves "the bytes exist" without claiming
		// integrity verification. Read across replicas to avoid a stale local stat
		// 404-ing a NAR a peer just uploaded.
		field.Time("bytes_stored_at").
			Optional().
			Nillable(),
		// dechunk_residue_flagged_at records when fsck first observed this row to be
		// an un-de-chunkable chunked NAR (CDC residue). NULL means "not flagged". fsck
		// sets it on first detection, clears it when the row becomes recoverable or is
		// de-chunked, and uses its age (against a configurable grace window) to decide
		// deferred reclamation. Independent of verified_at, bytes_stored_at, and
		// chunking_started_at.
		field.Time("dechunk_residue_flagged_at").
			Optional().
			Nillable(),
		field.Time("last_accessed_at").
			Optional().
			Nillable().
			Default(time.Now).
			// DB default declared via entsql.Default (a string default Atlas's
			// SQLite inspector round-trips exactly) rather than
			// entsql.Annotation{DefaultExpr: ...} (a parenthesized RawExpr the
			// inspector strips, producing a perpetual phantom table rebuild —
			// issue #1328). Mirrors the Timestamps mixin's created_at.
			Annotations(entsql.Default("CURRENT_TIMESTAMP")),
	}
}

// Edges of the NarFile.
//
// Both narinfo_nar_files and nar_file_chunks are modelled as plain edges
// to their join entities (not Ent's Through pattern) because the join
// entities use surrogate `id` PKs.
func (NarFile) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("nar_info_nar_files", NarInfoNarFile.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("chunk_links", NarFileChunk.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the NarFile.
func (NarFile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("hash", "compression", "query").Unique(),
		index.Fields("last_accessed_at"),
	}
}
