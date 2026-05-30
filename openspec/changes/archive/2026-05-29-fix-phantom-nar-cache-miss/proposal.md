## Why

Clients pulling the 26.05 release get persistent `error: file 'nar/<hash>.nar.xz' does not exist in binary cache` for paths whose `.narinfo` ncps just served `200`. Production logs show `nar_file` rows are written at narinfo-fetch time (before any bytes exist), and the read path treats "a row exists" as "we have the NAR." When the background CDC download fails — which it does for ~87% of pulls under load (1418 started, 186 completed; GOAWAY / http2 timeouts) — the row survives with `total_chunks=0` and `chunking_started_at=NULL`, and every later `GET /nar` returns a permanent ~2 ms 404 instead of re-fetching upstream. A single failed download poisons the NAR forever. Secondary symptoms: truncated bodies (progressive CDC streaming emits a short body) and nginx 504s on slow upstream pulls.

## What Changes

- **Read-path recovery (core fix):** `GetNar` MUST distinguish a *servable* NAR (whole-file in store, or `total_chunks>0`, or chunking actively in progress) from a *backing-less* `nar_file` row. A backing-less row MUST be treated as a cache **miss** that triggers a synchronous upstream re-download, never a terminal 404.
- **Phantom-record prevention:** stop persisting `nar_file` rows as authoritative before backing data exists, or mark such placeholder rows non-servable so they cannot satisfy `HasNarFileRecord`-style checks. Stuck records (chunking aborted/stale) MUST self-heal on the next request rather than poison it.
- **Streaming integrity:** progressive CDC streaming MUST NOT deliver a truncated `200` body; when chunks never materialize it MUST surface a retryable error / fall back to re-download rather than a short read.
- **Upstream resilience:** harden background and synchronous NAR pulls against transient GOAWAY / http2-timeout / broken-pipe so downloads succeed more often and a transient failure never persists a poisoning record.
- This is **not** a wire/protocol change; no **BREAKING** changes.

## Capabilities

### New Capabilities
- `nar-cache-miss-recovery`: `GET /nar` never returns a permanent 404 for a NAR that upstream can still provide; backing-less `nar_file` rows are cache misses that trigger a synchronous upstream re-download, and transient upstream failures do not create poisoning records.

### Modified Capabilities
- `cdc-chunking`: placeholder/phantom `nar_file` rows (`total_chunks=0`, `chunking_started_at=NULL`, no bytes) MUST NOT be treated as servable; stuck/aborted chunking records MUST be recoverable instead of permanently failing reads.
- `nar-concurrent-streaming`: progressive streaming MUST NOT emit truncated NAR bodies; on missing chunks it falls back to re-download or a retryable error.

## Impact

- Code: `pkg/cache/cache.go` (`GetNar`, `serveNarFromStorageViaPipe`, `getNarFromChunks`, `streamProgressiveChunks`, `coordinateDownload`/`prePullNar` `hasAsset`, `storeInDatabase`/`createOrUpdateNarFileEnt`), `pkg/server/server.go` nar handler.
- No schema migration expected (behavioral change over existing `nar_file` columns); confirm during design.
- **I/O / network:** more upstream re-fetches for previously-poisoned paths (one-time recovery cost), then normal cache behavior. No added steady-state I/O for healthy NARs.
- **Memory:** negligible; no new buffering beyond existing streaming paths.
