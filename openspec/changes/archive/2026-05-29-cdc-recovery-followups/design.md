## Context

Follow-ups to the archived `fix-phantom-nar-cache-miss` change. The read path and the
CDC lazy-recovery sweep are now correct: backing-less placeholder `nar_file` rows are
non-servable (`isServable`), re-downloaded on demand by `GetNar`, and the sweep skips
them (it can only migrate store-present NARs) while a keyset cursor (`fa30dc39`)
prevents head-of-line starvation. This change addresses what is left: dead placeholder
rows are never reclaimed, two streaming requirements lack tests, and transient upstream
retries have no backoff.

Constraints: production is live; migrations forward-only / expand-contract
(`.claude/rules/ent-migrations.md`); no-panic-outside-main; TDD required; tests run
with the race detector and must call `t.Parallel()`.

## Goals / Non-Goals

**Goals:**
- Bound the lifetime of provably-unrecoverable backing-less placeholder rows so they
  do not accumulate or get re-scanned forever.
- Pin the progressive-streaming abort/stall behavior with a dedicated regression test.
- Add bounded backoff to transient upstream retries without making genuine 404s
  retryable.

**Non-Goals:**
- No change to the read-path recovery, `isServable`, or the keyset cursor (already
  correct).
- No new client-visible endpoints or wire changes.
- Not eliminating the placeholder row at narinfo-fetch time (that design was settled
  in fix-phantom-nar-cache-miss: keep the row, rely on `isServable`).

## Decisions

### D1: GC only provably-unrecoverable placeholder rows
A backing-less placeholder row is collected only when BOTH hold: it is not in the store
(`!HasNarInStore`) and upstream definitively does not have it. "Definitively" must
distinguish a real not-found from a transient/timeout — reusing the existing
`upstream.Cache.HasNar` is insufficient as-is because it returns `(false, nil)` on
timeout too. Either add an absence check that only reports true on a confirmed not-found
status, or require N consecutive confirmations across sweeps before deleting.
- *Why:* never delete a row whose NAR upstream can still serve — `GetNar` must remain
  able to re-create and download it. Over-eager deletion would just be re-created on the
  next request (churn) or, worse, race a live download.
- *Alternative:* age-out purely by `created_at` + absent narinfo, no upstream probe —
  simpler but risks deleting rows for NARs upstream still has during an outage.

### D2: Reclaim the FK link atomically
Deleting a `nar_file` row must also remove its `narinfo_nar_files` link (and must not
orphan or violate FK constraints). Do the delete + link cleanup in one transaction,
mirroring existing nar_file deletion paths. Confirm whether existing cascade behavior
covers this or an explicit delete is needed.

### D3: Bounded backoff in doRequest
Add a small capped backoff (e.g. exponential with a low ceiling, jittered) between
transient retries in `upstream.doRequest`, gated on `isRetriableTransportError`. Respect
`ctx` cancellation during the wait. Genuine 404 returns a response (not a transport
error) and is therefore never retried — unchanged.
- *Why:* immediate count-bounded retries hammer an upstream that is brown-out failing.
- *Open:* exact ceiling/attempts; keep total added latency well under typical client
  timeouts.

### D4: Streaming abort/stall test is pure coverage
The test drives `streamProgressiveChunks`/`getNarFromChunks` into (a) `total_chunks=0 &&
chunking_started_at==NULL` (aborted) and (b) a producer that never advances past
`cdcChunkingLockTTL` (stalled), asserting an error is surfaced and no short body is
closed as success. No production change unless the test reveals a real gap.

## Risks / Trade-offs

- **Deleting a row for a NAR upstream still has** → churn or a race with a live
  download. Mitigation: D1's definitive-absence gate + only operating on rows older than
  the recovery cutoff; both old and new pods share the DB, so the delete must be safe
  under concurrent serving.
- **GC adds upstream HEAD load** → mitigation: bounded by batch size / recovery interval
  and only for backing-less rows.
- **Backoff worsens tail latency on a failing fetch** → mitigation: low ceiling + cap on
  attempts; genuine 404 path is untouched.

## Migration Plan

- Code-only expected. Placeholder GC deletes dead rows (and links) at runtime — no
  schema change. If a marker column (e.g. consecutive-absence count) is needed for D1,
  add it nullable first per expand-contract.
- Rolling deploy safe: GC only removes rows that are non-servable and confirmed absent;
  old pods continue to re-download on demand.

## Open Questions

- D1: probe-on-sweep vs a persisted consecutive-absence counter — which better avoids
  deleting during a transient upstream outage without a schema change?
- D2: does the current Ent schema cascade `narinfo_nar_files` on `nar_file` delete, or is
  an explicit transactional cleanup required?
- D3: backoff ceiling and max attempts that meaningfully reduce brownout load without
  pushing fetch latency past nginx/client timeouts?
