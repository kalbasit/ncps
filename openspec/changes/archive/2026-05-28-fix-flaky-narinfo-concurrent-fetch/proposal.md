## Why

`TestGetNar_NixServeUpstream` has three parallel subtests that all fetch the same narinfo hash concurrently; when ncps's narinfo fetch-and-store path is not protected against concurrent requests for the same hash, competing goroutines can each receive "not found" and race to fetch from upstream — causing transient 404s in the subtests that lose the race. This manifests as a CI-only flake in the Nix sandbox derivations (`ncps-postgres-tests`, `ncps-s3-tests`, `ncps-mysql-tests`) where all subtests start simultaneously with a cold cache.

## What Changes

- Add a per-hash in-flight deduplication guard (e.g. `golang.org/x/sync/singleflight`) to the narinfo fetch-and-store path so concurrent requests for the same hash coalesce onto a single upstream fetch.
- The test itself (`TestGetNar_NixServeUpstream`) requires no structural change — the fix makes the server behave correctly under parallel load.

## Capabilities

### New Capabilities

- `narinfo-concurrent-fetch`: Coalescing / deduplication of concurrent upstream narinfo fetches for the same hash, ensuring at most one in-flight fetch per hash at any given time.

### Modified Capabilities

_(none — no existing spec-level requirements change)_

## Impact

- **`pkg/cache/`**: Core narinfo fetch-and-store logic gains a `singleflight.Group` (or equivalent) guard around the upstream fetch + local store sequence.
- **`pkg/server/`**: No handler changes; the fix is in the cache layer below the HTTP handler.
- **Tests**: The existing parallel subtests in `pkg/server/server_test.go` (`TestGetNar_NixServeUpstream`) will pass reliably without modification.
- **Memory**: Negligible — `singleflight.Group` holds one in-flight result entry per hash during the fetch window only.
- **Latency**: No added latency for the common (cache-warm) case; coalesced concurrent requests share one upstream round-trip instead of each making their own.
- **Dependencies**: `golang.org/x/sync/singleflight` is already in the Go ecosystem standard library extension; no new external dependencies required.
