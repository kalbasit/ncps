## Context

ncps serves a NAR in two stages. `GET /{hash}.narinfo` fetches the narinfo from upstream and
calls `storeInDatabase` (`pkg/cache/cache.go` ~L4418), which creates a `nar_file` row via
`createOrUpdateNarFileEnt` with only `hash`, `compression`, `query`, `file_size` set —
`total_chunks = 0` and `chunking_started_at = NULL`. It then kicks off a background NAR
download/chunk. `GET /nar/{hash}` later calls `GetNar` (L1001+), which decides servability from
`HasNarInStore`, `HasNarInChunks` (`total_chunks > 0`), and a CDC progressive-streaming check.

The defect: when the background download fails — common under load (logs: 1418 starts, 186
completions; upstream `GOAWAY` / `http2: timeout awaiting response headers` / broken pipe) — the
`nar_file` row survives as a backing-less placeholder. `coordinateDownload`'s `hasAsset` and the
read path treat row existence as "we have it" in at least one path, and `GetNar` ends up calling
`serveNarFromStorageViaPipe(hasNarInStore=false)` → `getNarFromChunks` → `streamProgressiveChunks`,
which sees `total_chunks=0 && chunking_started_at==NULL` and returns `storage.ErrNotFound` in ~2 ms.
Result: a single failed download permanently poisons the NAR — every future `GET /nar` 404s instead
of re-fetching upstream. Secondary symptoms: truncated 200 bodies from progressive streaming and
nginx 504s from slow pulls.

Constraints: production is live; migrations are forward-only and expand-contract (per
`.claude/rules/ent-migrations.md`). No-panic-outside-main applies. TDD is required.

## Goals / Non-Goals

**Goals:**
- `GET /nar` never returns a terminal 404 for a NAR upstream can still provide; backing-less
  records become cache misses that re-download synchronously.
- Placeholder/stuck `nar_file` rows are never treated as servable and can self-heal.
- Progressive CDC streaming never emits a truncated successful body.
- Transient upstream failures (GOAWAY/timeout/reset) do not persist poisoning records.

**Non-Goals:**
- No NAR wire/protocol changes; no new client-visible endpoints.
- Not rewriting the CDC chunking algorithm or fastcdc dependency.
- Not eliminating upstream 504s at the nginx layer (out of ncps's process); only reducing the
  upstream-pull failure rate that feeds them.
- No new database columns unless design review proves them necessary.

## Decisions

### D1: Single source of truth for "servable" — `isServable(narURL)`
Introduce one helper that answers whether a NAR can be served right now: whole-file in store,
OR `total_chunks > 0`, OR chunking actively in progress (`chunking_started_at` within
`cdcChunkingLockTTL`). Every read-path decision (`GetNar`'s CDC check at L1052-1062,
`coordinateDownload`'s `hasAsset`, and any `HasNarFileRecord`-based gate) routes through it.
- *Why:* the bug is divergent, ad-hoc servability checks. One predicate removes the class of bug.
- *Alternative considered:* patch each site independently — rejected as fragile and the reason
  the regression recurred across #1255/#1263/#1279/#1290.

### D2: Backing-less record ⇒ cache miss ⇒ synchronous re-download
In `GetNar`, when `!isServable` and not in upload-only mode, always fall through to the
prePull/coordinateDownload path (never short-circuit to `ErrNotFound`). `hasAsset` returning
`false` for placeholders (D1) guarantees `coordinateDownload` actually downloads rather than
returning a `closed` state.
- *Why:* restores the pre-CDC behavior of lazy upstream fetch on miss.
- *Alternative:* serve stale/empty — unacceptable (truncation).

### D3: Don't persist authoritative placeholders (or mark them)
Prefer NOT creating a `nar_file` row in `storeInDatabase` until the NAR is actually downloaded,
OR ensure the narinfo→nar_file link plus an explicit non-servable state is distinguishable from a
real record. Chosen direction: keep the row (it carries `file_size` and the narinfo link) but rely
on D1 so its mere existence is never "servable." Investigate during implementation whether the
narinfo can be served without eagerly creating the `nar_file` row at all.
- *Why:* minimizes migration risk; `total_chunks`/`chunking_started_at` already encode state.
- *Alternative:* new `state` enum column — heavier (migration, expand-contract); revisit only if
  the existing columns can't express "placeholder vs stuck vs servable" cleanly.

### D4: Streaming integrity — fail loud, never short-200
`streamProgressiveChunks`/`getNarFromChunks` must, on "no chunks will arrive," (a) fall back to
synchronous re-download if no bytes have been committed to the client, else (b) surface an error so
the transfer is seen as failed. Never close a short body as success.
- *Why:* "truncated input" / "failed to read compressed data" on the client.

### D5: Upstream resilience
Extend existing retry (already handles `GOAWAY`) to also retry `http2: timeout awaiting response
headers` and connection resets for NAR GETs with bounded backoff, and guarantee a failed pull
leaves no poisoning record (ties to D2).

## Risks / Trade-offs

- **Re-download stampede** on previously-poisoned hashes after deploy → Mitigation: existing
  distributed `download:nar:` lock serializes per-hash; recovery is one-time per hash.
- **Self-healing loop on genuinely-absent NARs** (re-trying forever) → Mitigation: a real upstream
  404 must surface as 404 and not be retried in a tight loop (recovery job must distinguish 404
  from transient).
- **Behavior change to existing 404 requirement** (`cdc-chunking`: 404 for compressed URL when
  only chunks exist) → Mitigation: that case has `total_chunks > 0` (servable), untouched by D1;
  our changes only affect `total_chunks = 0` backing-less rows.
- **Hidden concurrency** between recovery job and live `GetNar` re-download → Mitigation: both go
  through `coordinateDownload`'s lock; D1 keeps `hasAsset` consistent.

## Migration Plan

- Code-only change expected; no schema migration unless D3 escalates to a new column (then follow
  expand-contract: add nullable column first).
- Deploy rolling; both old and new pods share the DB. New pods stop trusting placeholders; old
  pods' behavior is unchanged (no schema break).
- Rollback: revert the image; no data migration to undo. Existing poisoned rows simply remain
  404 under the old binary (status quo) until re-deployed.

## Open Questions

- Can the narinfo path defer `nar_file` row creation until first successful NAR download without
  breaking the narinfo→nar_file link, LRU `last_accessed_at`, or `file_size` reporting? (Drives
  D3's final shape.)
- Should `streamProgressiveChunks` fallback re-download inline, or signal `GetNar` to restart the
  serve attempt? (D4 implementation detail.)
- What backoff/cap is appropriate for D5 NAR-GET retries without worsening 504s under upstream
  brownouts?
