// Package migrations embeds the per-dialect goose-format SQL migration
// files under migrations/<dialect>/ into the ncps binary. At runtime,
// `ncps migrate up` selects the dialect-specific sub-FS via
// `fs.Sub(FS, "<dialect>")` and hands it to `goose.NewProvider`.
//
// Migration files are produced by `cmd/generate-migrations` (Atlas as a
// Go library) from the Ent schemas under `ent/schema/`. They are also
// kept under per-dialect integrity files (`atlas.sum`) — those files are
// produced and verified by Atlas; see `cmd/generate-migrations`.
package migrations

import "embed"

// FS embeds every file under migrations/sqlite, migrations/postgres, and
// migrations/mysql. Per-dialect access via:
//
//	fs.Sub(migrations.FS, "sqlite")
//	fs.Sub(migrations.FS, "postgres")
//	fs.Sub(migrations.FS, "mysql")
//
//go:embed sqlite/*.sql postgres/*.sql mysql/*.sql
var FS embed.FS
