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

// BuildTraceEntry holds one row per stored build-trace-v2 entry.
// A build trace entry maps a (drv_path, output_name) key to an out_path
// value, signed by one or more parties. raw_json stores the verbatim
// upload body as a forward-compatibility safety valve.
type BuildTraceEntry struct {
	ent.Schema
}

// Annotations pins the on-disk table name to "build_trace_entries".
func (BuildTraceEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "build_trace_entries"},
	}
}

// Mixin of BuildTraceEntry.
func (BuildTraceEntry) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the BuildTraceEntry.
func (BuildTraceEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("drv_path").NotEmpty(),
		field.String("output_name").NotEmpty(),
		field.String("out_path").NotEmpty(),
		field.Text("raw_json").NotEmpty(),
	}
}

// Edges of the BuildTraceEntry.
func (BuildTraceEntry) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("signatures", BuildTraceSignature.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the BuildTraceEntry.
func (BuildTraceEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("drv_path", "output_name").Unique(),
	}
}
