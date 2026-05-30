## 1. Backing-less placeholder row GC (cdc-chunking)

- [ ] 1.1 Add a definitive upstream-absence check (distinct from `upstream.Cache.HasNar`, which returns `(false, nil)` on timeout): report genuinely-absent only on a confirmed not-found status, transient/timeout as "unknown". Add a focused test.
- [ ] 1.2 Confirm whether deleting a `nar_file` row cascades `narinfo_nar_files` in the current Ent schema, or whether an explicit transactional link cleanup is required; document the finding (resolves design Open Question D2).
- [ ] 1.3 Write a failing test: a backing-less placeholder row (older than cutoff, no store file) for a genuinely-absent hash is removed (row + link gone) by the recovery process.
- [ ] 1.4 Implement GC in the recovery path: for a backing-less stuck row confirmed genuinely absent upstream, delete the row and its link in one transaction. Make 1.3 pass.
- [ ] 1.5 Write a test: a backing-less placeholder whose NAR upstream still has is NOT removed, and a later `GetNar` re-downloads it.
- [ ] 1.6 Write a test: a transient/timeout upstream check does NOT remove the placeholder row (remains eligible later).
- [ ] 1.7 Bound the GC (batch size / recovery interval) and only consider rows older than the cutoff so it cannot race a fresh in-flight placeholder; add/extend a test asserting fresh rows are untouched.

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
