package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BuildTraceSignature stores one row per (build_trace_entry, signature) pair.
// key_name is the signing key identifier (e.g. "cache.example.com-1");
// signature is the base64-encoded Ed25519 signature over the entry fingerprint.
type BuildTraceSignature struct {
	ent.Schema
}

// Annotations pins the on-disk table name to "build_trace_signatures".
func (BuildTraceSignature) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "build_trace_signatures"},
	}
}

// Fields of the BuildTraceSignature.
func (BuildTraceSignature) Fields() []ent.Field {
	return []ent.Field{
		field.Int("build_trace_entry_id"),
		field.String("key_name").NotEmpty(),
		field.String("signature").NotEmpty(),
	}
}

// Edges of the BuildTraceSignature.
func (BuildTraceSignature) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("build_trace_entry", BuildTraceEntry.Type).
			Field("build_trace_entry_id").
			Ref("signatures").
			Unique().
			Required(),
	}
}

// Indexes of the BuildTraceSignature.
func (BuildTraceSignature) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("build_trace_entry_id", "key_name").Unique(),
		index.Fields("build_trace_entry_id"),
	}
}
