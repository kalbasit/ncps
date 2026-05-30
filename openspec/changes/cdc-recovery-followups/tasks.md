## 1. Backing-less placeholder row GC (cdc-chunking)

- [x] 1.1 Added a definitive tri-state probe `upstream.Cache.NarInfoExistence(hash) → Existence{Present,Absent,Unknown}` (timeout/transport/5xx → Unknown; 404 → Absent). Probes the *narinfo* path (unambiguous), not the NAR path whose CDC-normalized compression would falsely 404. (`TestNarInfoExistence`.)
- [x] 1.2 Confirmed: deleting a `nar_file` cascades `narinfo_nar_files` AND `nar_file_chunks` via `ON DELETE CASCADE` (FK in migrations + `entsql.OnDelete(entsql.Cascade)` on edges). `DeleteOneID` suffices — no manual link cleanup.
- [x] 1.3 `TestRecoveryGCDeletesGenuinelyAbsentPlaceholder`: a backing-less placeholder (older than cutoff) whose narinfo 404s on every healthy upstream is deleted.
- [x] 1.4 Implemented `gcOrSkipBackingLessNarFile` + `narInfoGenuinelyAbsentUpstream` in `runCDCLazyRecovery`: resolve the narinfo by URL, probe all healthy upstreams, delete only when EVERY healthy upstream is definitively Absent (≥1 healthy upstream, none Present/Unknown).
- [x] 1.5 `TestRecoveryGCKeepsPlaceholderPresentUpstream`: a placeholder whose narinfo is still present upstream is NOT deleted.
- [x] 1.6 `TestRecoveryGCKeepsPlaceholderOnTransientProbe`: a 503/Unknown probe does NOT delete the placeholder.
- [x] 1.7 `TestRecoveryGCSkipsFreshPlaceholder`: GC only considers rows older than the recovery cutoff (`CreatedAtLT`), so a fresh in-flight placeholder is never deleted even if genuinely absent.

## 2. Progressive-streaming abort/stall regression test (nar-concurrent-streaming)

- [x] 2.1 Add a test driving `streamProgressiveChunks`/`getNarFromChunks` into the aborted state (`total_chunks=0 && chunking_started_at==NULL`): assert an error is surfaced and no short body. (`TestGetNarFromChunks_AbortedChunkingErrorsNotShortBody` — passed unchanged; characterization guard.)
- [x] 2.2 Add a test for the stalled-producer state (`chunking_started_at` older than `cdcChunkingLockTTL`, `total_chunks=0`, no chunks): assert the streaming path surfaces an error rather than blocking. (`TestGetNarFromChunks_StalledChunkingFailsFast`.)
- [x] 2.3 Real gap found and fixed: `streamProgressiveChunks` only failed a stalled stream after the 30s per-chunk timeout. Added a stale-lock fast-fail (`time.Since(chunking_started_at) > cdcChunkingLockTTL → ErrNotFound`) at both the initial and wait-loop record checks, so a dead producer fails in ~200ms instead of 30s.

## 3. Bounded backoff for upstream transient retries (upstream-fetch-resilience)

- [x] 3.1 Write a failing test: a GET that fails repeatedly with a transient transport error is retried with a measurable, capped backoff between attempts (not immediate), bounded in count. (`TestDoRequest_RetriesUseBackoff`)
- [x] 3.2 Implement capped backoff in `upstream.doRequest` between transient retries (gated on `isRetriableTransportError`, applies to GOAWAY too), respecting `ctx` cancellation. (`waitRetryBackoff`; base/cap via `defaultRetryBackoff*`, override `Options.RetryBackoff`.)
- [x] 3.3 Write a test: cancelling the context during a backoff wait returns promptly with the context error. (`TestDoRequest_BackoffRespectsContextCancellation`)
- [x] 3.4 Confirm a genuine 404 is still not retried and incurs no backoff: 404 returns a response (not a transport error), so `waitRetryBackoff` is never reached. (Covered by `TestDoRequest_GenuineNotFoundIsNotRetried`.)

## 4. Verify & finalize

- [ ] 4.1 Run `task fmt` and confirm it exits zero with no pending changes.
- [ ] 4.2 Run `task lint` and confirm changed packages are clean (every `//nolint` carries a comment).
- [ ] 4.3 Run `task test` (cache + upstream under `-race`) and confirm all pass.
