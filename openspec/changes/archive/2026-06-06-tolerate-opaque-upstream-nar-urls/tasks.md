## 1. URL parsing (pkg/nar)

- [x] 1.1 Write failing unit tests for `ParseUpstreamURL`: hash-named URL behaves like `ParseURL`; opaque (UUID) URL preserves the upstream path and keys off the fallback hash; opaque URL with an invalid fallback hash errors; structurally invalid URL errors even with a valid fallback
- [x] 1.2 Write a failing test for `WithOpaquePath` round-trip (`IsOpaque`/`OpaquePath`)
- [x] 1.3 Refactor URL splitting into `parseURLParts` (no hash policy) shared by `ParseURL` and `ParseUpstreamURL`
- [x] 1.4 Add unexported `opaquePath` field, `ParseUpstreamURL`, and `IsOpaque`/`OpaquePath`/`WithOpaquePath` accessors
- [x] 1.5 Make `pathWithCompression` return the opaque path when set, while `ToFilePath` stays keyed off `Hash`; confirm tests pass

## 2. Data model (ent + migrations)

- [x] 2.1 Add nullable `upstream_url` field to `ent/schema/narinfo.go`
- [x] 2.2 Run `task ent:generate` to regenerate the Ent client
- [x] 2.3 Generate the additive forward-only migration via `task migrations:gen NAME=add_narinfo_upstream_url` for sqlite/postgres/mysql; verify each is a plain `ADD COLUMN`
- [x] 2.4 Run `task ent:check` and confirm it exits zero

## 3. Cache pull/serve/re-fetch (pkg/cache)

- [x] 3.1 Write a failing integration test (`TestGetNarInfoOpaqueUpstreamURL`): opaque upstream URL is proxied without 500, re-served under ncps's own hash-named URL, `upstream_url` persisted, and the evicted NAR is re-fetchable via the persisted opaque path
- [x] 3.2 Add `narInfoStorageKey` (NarHash → bare nix32 digest fallback key)
- [x] 3.3 Switch `pullNarInfo` and `lookupPreferredUpstreamURL` to `ParseUpstreamURL`; snapshot the opaque path before the URL rewrite and copy `narURL` before the background fetch
- [x] 3.4 Add the opaque-URL `switch` case that rewrites `narInfo.URL` to ncps's hash-named URL (preserving compression) without leaking the opaque path
- [x] 3.5 Add best-effort `setNarInfoUpstreamURL` and persist the opaque path after `storeInDatabase`
- [x] 3.6 Restore the opaque path in `lookupOriginalNarURL` via `WithOpaquePath` for post-eviction re-fetch; confirm the integration test passes

## 4. Verification

- [x] 4.1 `task fmt`, `task lint`, and `task test` all exit zero
