## Context

A fresh client hit three server-side faults serving NARs from ncps: mass fast
`404 does not exist in binary cache`, mid-stream `unexpected end of nar` /
`truncated input` (HTTP/2 stream resets), and nginx `504`. The production
deployment runs the `local` (POSIX) storage backend on a single NFS share
(`ReadWriteMany`) shared by **two** replicas, with CDC enabled — a topology that
violates the single-writer/strong-consistency assumption the `local` driver
makes, and that turns each NAR into many small chunk reads over a high-latency
spinning backend. The topology aggravates the faults; the faults themselves are
ncps bugs against requirements that already exist (`nar-cache-miss-recovery`,
`nar-concurrent-streaming`).

Relevant current code (`pkg/cache/cache.go`):

- `GetNar` (1008) already *intends* to recover: when `isServable` is false it
  falls through to `prePullNar` and re-downloads rather than 404-ing.
- `isServable` (3873) returns `true` whenever the `nar_file` record is servable
  (`total_chunks > 0` / chunking in progress) **without confirming the chunk
  bytes actually exist in storage**. On a backend where the DB (fast SSD) is
  ahead of the bytes (slow/stale NFS), this commits the `200` and then
  `getNarFromChunks` fails mid-stream → truncated body.
- The chunk prefetch loop (≈7395–7445) waits `maxWaitPerChunk` (~30s,
  hard-coded) per chunk, then emits `timeout waiting for chunk N`. Because the
  response is already committed, the stall surfaces as truncation / eventually a
  gateway 504 (nginx `proxy-read-timeout: 300s`).
- `HasNarInStore` returns a bare `bool`, conflating "confirmed absent" with
  "could not determine" (e.g. a transient/stale NFS stat error). The narinfo
  purge guard (4198) keys its destructive decision on that boolean.

## Goals / Non-Goals

**Goals:**

- A narinfo/NAR whose backing bytes are genuinely absent is recovered from
  upstream on demand, never answered with a hard 404 or a destructive purge
  driven by an ambiguous storage result.
- The chunk-serving path verifies the chunk set is present and byte-complete
  **before** committing the response status; a missing/short chunk set falls
  back to re-download while no bytes are committed.
- The chunk-wait / serving deadline is bounded and operator-configurable, with a
  default that fits inside a typical gateway timeout; a post-commit stall aborts
  the transfer (client sees a failed download) rather than closing a short body.
- Regression tests reproduce each fault first (TDD), including a backend that can
  simulate "DB record present, bytes missing/slow" and "stat returns an
  ambiguous error."
- Constructive operator documentation on backend selection.

**Non-Goals:**

- No redesign of CDC chunking, the Redis download lock, or upstream
  retry/backoff.
- No `migrate-chunks-to-nar` (de-chunking) tool — separate change, separate
  branch.
- No nginx/ingress changes; ncps stays within the gateway's timeout.
- No change that depends on fixing the user's topology — the code must degrade
  cleanly regardless.

## Decisions

### 1. Distinguish "confirmed absent" from "could not determine"

Introduce a storage-presence check that returns a tri-state (or
`(present bool, err error)`) instead of swallowing errors into a bare `bool`.
Confirmed `ErrNotFound` → absent (eligible for recovery/purge). Any other error
(timeout, stale handle, transport) → **unknown**: do **not** treat as absence,
do **not** purge; serve-if-possible else surface a retryable error so the next
request re-evaluates. The narinfo purge guard (4198) and `isServable` consume
this distinction. This directly addresses the cross-pod NFS false-miss without
ncps pretending NFS is consistent.

### 2. Verify chunk-set integrity before committing the response

Before `serveNarFromStorageViaPipe`/`isServable` commit to the chunk path, add a
pre-flight that enumerates the required chunks and confirms (a) every referenced
chunk object is present and (b) the chunk byte total equals the NAR's declared
size. If the pre-flight fails while no bytes are committed, fall back to
synchronous upstream re-download (the existing `prePullNar` path) instead of
streaming a body that cannot be finished. Pre-flight cost is metadata-only
(existence/size), not a full read; it trades a small up-front check for
eliminating truncated `200`s.

### 3. Bound and expose the chunk-wait / serving deadline

Replace the hard-coded `maxWaitPerChunk` with a configured value (new key under
`cache.download`, e.g. `chunk-wait-timeout`, default chosen to keep total
serving time < a typical gateway timeout). Add an overall serving deadline so a
sequence of slow chunks cannot accumulate past the gateway. On deadline expiry
after commit, terminate the stream abnormally (so the client observes a failed
transfer and retries) — never close at the truncation point as a clean EOF.

### 4. TDD reproduction before fixes

The exact origin of the ~1.5 ms 404 is not yet pinned to a single line (GetNar's
fall-through suggests it should recover; candidates include the
`.nar.xz`-vs-uncompressed-chunk servability mismatch and the narinfo-endpoint
purge path). The first task is a failing test using a storage fake that models
"record present / bytes absent" and "ambiguous stat error," asserting recovery
and no-truncation. The fix is shaped by what that test exposes.

### 5. Documentation

Add backend-selection guidance (README/deployment docs and/or chart notes):
`local` assumes single-writer POSIX semantics; multi-replica deployments should
use an object-store backend; CDC suits low-latency storage. Phrased as
what-to-do-and-why, not a post-mortem.

## Risks / Trade-offs

- **Pre-flight adds metadata round-trips per chunked serve.** On the very
  backend that is slow, this adds latency — but only existence/size checks, and
  it prevents the far worse truncated-`200`. Mitigation: batch the existence
  check; skip pre-flight when serving a whole-file (non-chunked) path.
- **Recovery-instead-of-404 can mask a genuinely-gone NAR as a slow request.**
  Bounded by decision #3 (deadline) and the existing "genuinely absent upstream
  → 404" requirement.
- **Tri-state presence touches several call sites** (`isServable`,
  `serveNarFromStorageViaPipe`, purge guard). Risk of behavior drift; covered by
  the new scenarios plus existing `nar-cache-miss-recovery` /
  `nar-concurrent-streaming` tests.
- **New config key** must default safely so existing deployments need no change;
  the default must be < common gateway timeouts (e.g. nginx 300s) yet generous
  enough for legitimately slow upstreams.
