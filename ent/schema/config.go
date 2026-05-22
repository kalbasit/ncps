// Package schema is the Ent schema source for ncps. Schemas declared here
// drive both the generated Ent client (under ent/) and the per-dialect
// migration files (under migrations/) generated via cmd/generate-migrations.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/kalbasit/ncps/internal/entmixin"
)

// ConfigEntry stores arbitrary key/value configuration (notably the secret
// signing key) in the `config` table. The Go type name is ConfigEntry
// because Ent reserves `Config` as a predeclared identifier in its
// generated client.
type ConfigEntry struct {
	ent.Schema
}

// Annotations pins the on-disk table name to "config" so the rename of
// the Go type to ConfigEntry does not change the SQL schema.
func (ConfigEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "config"},
	}
}

// Mixin of ConfigEntry.
func (ConfigEntry) Mixin() []ent.Mixin {
	return []ent.Mixin{entmixin.Timestamps{}}
}

// Fields of the ConfigEntry.
func (ConfigEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty(),
		field.String("value").NotEmpty(),
	}
}

// Indexes of the ConfigEntry.
func (ConfigEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Unique(),
	}
}
