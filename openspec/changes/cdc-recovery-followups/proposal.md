## Why

The `fix-phantom-nar-cache-miss` change (archived) and its keyset-cursor follow-up
(`fa30dc39`) resolved the production phantom-404 defect and the head-of-line
starvation it introduced. Three lower-priority residuals remain, captured here so
they are tracked rather than lost:

1. **Backing-less placeholder rows are never cleared.** `runCDCLazyRecovery` now
   *skips* backing-less stuck `nar_file` rows (`total_chunks=0`,
   `chunking_started_at=NULL`, no whole-file in store) instead of clearing them. For
   NARs that are genuinely absent upstream, those placeholder rows persist in the DB
   indefinitely and are re-scanned (a cheap `HasNarInStore` stat) on every full
   keyset-cursor cycle, slowly accumulating unbounded dead rows.
2. **Progressive-streaming abort/stall is verified by inspection only.** The
   `nar-concurrent-streaming` requirements "aborted chunking must not yield a short
   200" and "stalled producer treated as failure" hold in the current code
   (`streamProgressiveChunks` errors when `total_chunks=0 && chunking_started_at==NULL`),
   but no dedicated test pins them, so a refactor could silently regress them.
3. **Upstream transient retries have no backoff.** `isRetriableTransportError` now
   retries GOAWAY, http2 header timeout, connection reset, and broken pipe, but with
   no delay between attempts — under a real upstream brownout this hammers the
   upstream with immediate retries.

None of these are correctness blockers today; they are hardening/cleanup follow-ups.

## What Changes

- **Placeholder GC (cdc-chunking):** add bounded garbage collection of truly-stale
  backing-less `nar_file` placeholder rows so they stop accumulating and being
  re-scanned forever. A row is only collected when it is provably unrecoverable —
  e.g. confirmed genuinely absent upstream (`upstream.Cache.HasNar` HEAD returns a
  definitive not-found) and/or its narinfo is gone — never when the NAR still exists
  upstream (`GetNar` must still be able to re-create and download it on demand). FK
  links (`narinfo_nar_files`) MUST be handled cleanly.
- **Streaming abort/stall regression test (nar-concurrent-streaming):** add a test
  that drives `streamProgressiveChunks`/`getNarFromChunks` into the aborted and
  stalled-producer states and asserts an error is surfaced — never a truncated HTTP
  200 closed as success.
- **Bounded retry backoff (upstream resilience):** add a small capped backoff
  between transient `doRequest` retries (applies to GOAWAY too). Genuine 404 stays
  non-retryable.
- No wire/protocol changes; no **BREAKING** changes.

## Capabilities

### Modified Capabilities
- `cdc-chunking`: backing-less placeholder `nar_file` rows that are provably
  unrecoverable MUST be garbage-collected (bounded, safe) rather than persisting and
  being re-scanned indefinitely; collection MUST NOT remove a row whose NAR upstream
  can still provide.
- `nar-concurrent-streaming`: the existing abort/stall-must-not-truncate requirements
  gain explicit, dedicated test coverage (no behavioral change).

### New Capabilities
- `upstream-fetch-resilience`: transient upstream transport failures on idempotent
  requests are retried with bounded backoff (capped delay, count-limited), while
  genuine not-found responses are never retried.

## Impact

- Code: `pkg/cache/cache.go` (`runCDCLazyRecovery` placeholder GC), possibly a new
  small helper for upstream-absence probing; `pkg/cache/upstream/cache.go`
  (`doRequest` backoff); new tests in `pkg/cache` and `pkg/cache/upstream`.
- DB: deletes of dead placeholder rows (+ their `narinfo_nar_files` links). No schema
  change expected; if one proves necessary it MUST be forward-only/expand-contract.
- I/O / network: GC may add bounded upstream HEAD probes for stale placeholders
  (rate-limited by the recovery interval and batch size). Backoff slightly increases
  worst-case latency on a failing fetch but reduces upstream load under brownouts.
- Risk: low; all items are additive hardening. Deleting placeholder rows must be
  guarded so a transiently-unreachable upstream is never treated as genuine absence.
