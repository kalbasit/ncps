package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/kalbasit/ncps/internal/entmixin"
)

// StagingState tracks the cross-pod coordination state for in-flight NAR
// staging (change serve-whole-nar-in-flight). There is at most one row per NAR
// hash. It is keyed by hash alone — independent of nar_files — because it must
// be writable during the active-download window, before any nar_file row may
// exist. A waiter records a staging request; the download holder advances
// parts_available and status as it writes part-objects to shared storage.
//
// status values (plain string, not an enum, to stay dialect-portable):
//   - "requested": a cross-pod waiter has asked for staging; holder not yet staging.
//   - "staging":   the holder is actively writing part-objects.
//   - "complete":  all part-objects for the NAR have been written.
//   - "abandoned": the holder died/failed; the record is eligible for sweep.
type StagingState struct {
	ent.Schema
}

// Annotations declares the on-disk table name and a table-level CHECK that
// parts_available is non-negative (field-level checks are dropped by Ent
// codegen — see ent-migrations invariant #1).
func (StagingState) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "staging_states"},
		entsql.Annotation{
			Checks: map[string]string{
				"staging_states_parts_available_nonneg": "parts_available >= 0",
			},
		},
	}
}

// Mixin contributes created_at / updated_at (created_at drives the GC sweep).
func (StagingState) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the StagingState.
func (StagingState) Fields() []ent.Field {
	return []ent.Field{
		field.String("hash").NotEmpty(),
		// requested_at records when a cross-pod waiter first requested staging.
		field.Time("requested_at").
			Optional().
			Nillable(),
		// parts_available is the count of durably-readable, sequentially-indexed
		// part-objects (indices 0..parts_available-1) currently in shared storage.
		field.Int64("parts_available").
			Default(0),
		// compression is the compression of the staged bytes (matches the holder's
		// temp file); readers transcode on serve when the client wants a different one.
		field.String("compression").
			Default(""),
		field.String("status").
			Default("requested"),
	}
}

// Indexes of the StagingState. One staging record per hash.
func (StagingState) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("hash").Unique(),
		index.Fields("created_at"),
	}
}
