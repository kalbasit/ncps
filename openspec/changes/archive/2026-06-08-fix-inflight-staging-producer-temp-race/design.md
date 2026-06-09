## Context

In-flight NAR staging engages when a lock-losing waiter records a `staging_state` "requested" marker and the download holder, seeing it, copies the in-flight NAR to shared storage as part-objects so the waiter serves complete bytes instead of a half-written stream. The `test-inflight-staging-contention-e2e.py` driver proved that on real, fast downloads the feature never serves, due to two coupled issues in `pkg/cache/inflight_staging.go`:

- `stagingActivationPollInterval = time.Second` (line 22): the holder's `waitForStagingRequest` polls `staging_state` once per second. The comment calls late activation "harmless" because of backfill-from-zero — but a download that finishes within one tick means the request is only observed via the `case <-ds.done` branch (line 163), i.e. *after* the download completed.
- `produceStagingParts` then reads `ds.assetPath` with `os.Open` (line 225). On completion the holder's temp file has already been moved into final storage, so the open fails with ENOENT and the producer logs a WARN error (`inflight_staging.go:132-136`) — staging never produces a single part.

`downloadState` (`pkg/cache/cache.go:540-547`) already carries the signals needed: `assetPath`, `finalSize` (0 until the download is done), `downloadError`, and the `done`/`start` channels.

## Goals / Non-Goals

**Goals:**
- The producer treats "download already completed / temp file gone" as a clean no-op, not an error — no spurious WARN, no failed activation.
- Activation engages promptly once a cross-pod waiter requests staging, so the feature actually serves during realistic contended downloads rather than only multi-second ones.
- Prove the fix with the existing contention e2e driver (green activation) plus focused `pkg/cache` unit tests, via TDD.

**Non-Goals:**
- Changing part size, retention, or the part-object format/layout.
- Touching the CDC/lazy chunking internals beyond what the chunking-window test exercises.
- Tuning the e2e driver's default package (a harness concern).

## Decisions

**D1 — Graceful no-op when the download already completed.**
Before opening the temp file, `produceStagingParts` checks whether the download has already finished (the request was observed via the `<-ds.done` path, or `ds.finalSize != 0` with the temp file gone). In that case it returns `nil` — the NAR is already committed to storage and waiters serve from there; staging-from-temp-file is moot. Additionally, an ENOENT from `os.Open(ds.assetPath)` is reclassified as "already committed" (clean return), never a WARN error. *Alternative considered:* keep erroring but downgrade the log to debug — rejected: it still counts as a failed producer and leaves staging_state in a misleading state; a clean no-op is the correct semantics.

**D2 — Shorten the holder's activation poll to match the waiter's cadence.**
Staging requests are recorded *only* by cross-pod waiters: `markStagingRequested` is called exclusively from `pollForDownloadOrTakeOver` (cache.go:6524), the lock-loser path. Same-process concurrent requests share the one `downloadState` and stream the shared temp file via `fileAvailableReader` — they never record a request. So there is no in-process signal to wake on; the holder learns of a waiter only by reading `staging_state`. The fix is therefore to lower `stagingActivationPollInterval` from `1s` to `200ms`, matching the waiter's own `pollInterval` (cache.go:6517), so the holder observes the request within ~200ms and stages while the download is still in flight. *Alternatives considered:* (a) a per-hash local signal on `downloadState` — rejected: inapplicable, since same-node waiters never request staging and cross-node ones can't reach an in-process channel; (b) a DB LISTEN/NOTIFY channel — Postgres-only, doesn't generalize to sqlite/mysql. A short poll is portable and, being holder-only and stopping on the first observed request, costs near zero.

**D3 — Keep backfill-from-zero.** Activation that lands mid-download still backfills the already-downloaded prefix from offset zero (unchanged behavior), so "prompt" only improves the window — it never changes correctness of the staged bytes.

## Risks / Trade-offs

- **[A shorter cross-node poll adds DB read load under contention]** → Only the holder polls, only while a download is in flight and no request has yet been seen; it stops the instant a request appears. Bounded and short-lived.
- **[Signal plumbing on `downloadState` risks a missed/duplicate wake]** → Treat the signal as a hint, not the source of truth: `waitForStagingRequest` always re-reads `staging_state` after a wake (and on the `<-ds.done` final check), so a missed signal degrades to the poll and a spurious signal degrades to a no-op read.
- **[ENOENT could mask a genuine storage fault]** → Scope the clean-return strictly to the "download already complete" condition (`ds.finalSize != 0` / `<-ds.done` observed); an ENOENT while the download is still in flight remains an error.

## Migration Plan

Pure behavior fix in `pkg/cache`; no schema, config, or API change. Ships as a normal deploy; rollback is a git revert. The e2e driver (already on the parent branch) is the multi-process acceptance gate.

## Open Questions

- Exact shortened cross-node poll interval (e.g. 100–250ms) — pick the largest value that still lands inside the e2e's contended-download window, to minimize idle DB reads.
- Whether to also emit a single debug line when staging no-ops because the download already completed, to keep the path observable without log noise.
