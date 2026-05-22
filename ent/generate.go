// Package ent is the generated Ent client root.
//
// The Ent client and supporting code under this directory are produced from
// the schemas in ./schema by `go generate ./ent/...` (equivalently
// `task ent:generate`). The generated tree is committed; CI verifies it is
// up to date via `git diff --exit-code ./ent/`.
package ent

//go:generate go tool ent generate --feature sql/upsert ./schema
