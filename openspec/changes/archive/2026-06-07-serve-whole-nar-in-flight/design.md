## Context

While a replica ingests a NAR, the only complete copy is a node-local temp file (`c.tempDir`), surfaced to **same-pod** readers via `fileAvailableReader` + the in-memory `c.upstreamJobs[lockKey]` `downloadState`. Cross-pod, there is no such channel.

Current cross-pod coordination (`pkg/cache/cache.go`):

- A waiter that loses `downloadLocker.Lock()` enters `pollForDownloadOrTakeOver` (`~6263`): every `200ms` it checks `hasAsset()` (finished NAR in storage?) and re-tries `TryLock` (holder died → take over), bounded by `max(downloadLockTTL, downloadPollTimeout)`, else a cache-miss (`storage.ErrNotFound`).
- The code deliberately **serializes**: *"Taking over only after re-acquisition keeps downloads serialized (at most one per hash across the cluster), so the concurrent-CDC path is never exercised by this fallback."* So a waiter's only outcomes today are **wait-for-complete** or **take-over-on-death** — never "read the in-flight bytes." Wait-for-complete on a large NAR is #660.

On S3 there is **no API to read an in-progress object** (a PUT / multipart upload is invisible until completion). Therefore the only way to serve in-flight bytes cross-pod is to commit *completed immutable objects* during the download. Granularity is the sole lever: one whole object (readable only at the end → #660) vs. many pieces (readable progressively).

The four download paths (`~3044`–`~3260`) produce chunks-during-download only in the eager-pipe path (compressed + known `NarSize` + `!lazy`); eager-simple chunks after download, lazy and non-CDC store the whole NAR after download. So a chunk-based cross-pod fast path exists in only 1 of 4 modes — the gap is CDC-mode-independent.

## Goals / Non-Goals

**Goals:**
- Serve a complete, byte-correct NAR cross-pod during the active-download window for **all** modes (non-CDC, lazy-CDC, eager-CDC) on S3 — closing #660.
- Subsume #1289: prefer a shared whole-NAR representation over `streamProgressiveChunks` during the eager-CDC chunking window.
- Zero cost when the feature is disabled, and zero cost when enabled but uncontended (single reader / slow-machine lazy download).
- Lock-losing waiters never return HTTP 500; on no staging, a correct bounded-wait + clean handoff.
- Make HA viable **without** CDC: the Helm chart accepts CDC *or* in-flight staging.

**Non-Goals:**
- Deduplication; changes to the CDC chunking algorithm, chunk-store layout, or `migrate-chunks-to-nar`.
- Crash-mid-chunking orphan recovery (#1230).
- Request-affinity routing as a correctness mechanism (optional documented optimization only).
- Always-on staging or forcing chunking on slow machines.

## Decisions

### D1 — Contention signal: a DB-backed `staging_state` record, polled on the loops both sides already run
`Cache` holds only the `lock.Locker` abstraction (Lock/Unlock/TryLock/Extend) — no raw Redis client — so `INCR`/`SADD`/pub-sub are not reachable without new plumbing. Meanwhile the **DB is already the cross-pod coordination substrate** (progressive chunks poll `nar_file_chunk`), and a waiter **already issues a DB query every 200 ms** via `hasAsset()` in `pollForDownloadOrTakeOver`.

So contention is signalled through a new additive `staging_state` table keyed by NAR hash (`hash`, `requested_at`, `parts_available`, `compression`, `status`, `created_at`), keyed by hash so it is writable during the active-download window when no `nar_file` row may exist yet:

- The **waiter** (B), on lock-loss, sets a one-shot "staging requested" marker in `staging_state` — read for free in the `hasAsset` query it already runs every 200 ms. Not a refreshing registry, so no added churn.
- The **holder** (A), in its download goroutine, polls `staging_state` on a coarse ticker (see D10) and, if requested *and* staging is enabled, activates.

- *Why:* reuses the existing DB-as-coordination pattern + the loop B already runs; durable across holder death (aids takeover and GC); testable on SQLite with no Redis; one additive table.
- *Alternatives:* **Redis keys** — lower latency but requires plumbing a raw go-redis client into `Cache`, splits behavior/tests local-vs-Redis, and gives a weaker takeover/GC story (ephemeral). **`INCR`/`SADD` refreshing registry** — moot once the marker is one-shot and the read is free on the existing poll. **pub/sub** — see D11.

### D2 — Staging representation: sequential fixed-size immutable part-objects in `NarStore`
A writes the in-flight NAR to shared storage as ordered, immutable, fixed-size part-objects under a staging key namespace, advancing `staging_state.parts_available` (D1) as each part lands. No content-defined boundaries, no rolling hash, no dedup bookkeeping.

- *Why:* the only S3-native way to expose in-flight bytes is completed immutable objects; fixed-size parts are ~free CPU (preserving lazy's purpose) and trivially ordered for sequential reassembly.
- *Alternatives:* **S3 multipart upload** — parts not readable until `CompleteMultipartUpload`. **One whole object after download** — readable only at completion = #660. **Reuse CDC chunks** — CPU-heavy, and absent in 3 of 4 modes.

### D2a — Part size: fixed 8 MiB default, configurable
Staging parts are a *transport* unit, not a dedup unit. CDC chunks are sized for dedup (min 16 KiB / avg 64 KiB / max 256 KiB), which would mean hundreds of objects + round-trips per NAR. Resolution: `--cache-inflight-staging-part-size` (env `CACHE_INFLIGHT_STAGING_PART_SIZE`), **default 8 MiB** (the conventional S3 multipart part size), explicitly independent of CDC chunk sizes.

- *Trade-off:* larger parts → fewer objects/round-trips but coarser tail granularity (a tailer blocks until a whole part is committed); 8 MiB balances both for typical NAR sizes.

### D3 — Activation is mid-download with backfill-from-zero
Contention typically appears *after* A starts. A does not decide at start; on first waiter detection it backfills the already-written temp prefix `[0, bytesWritten)` as parts, then appends parts as new bytes arrive. A late-arriving waiter still reads from part 0 → always a complete NAR.

- *Alternative:* decide-at-download-start — cannot serve the common "waiter arrives mid-download" case.

### D4 — Reader path: a third branch in the waiter loop, tailing parts
`pollForDownloadOrTakeOver` gains a branch: if `staging_state.parts_available > 0`, the waiter switches from wait-for-asset to **tailing parts** — a part-tailing reader analogous to `fileAvailableReader` but over shared part-objects + the `parts_available` marker. It inherits the `nar-concurrent-streaming` correctness contract: no truncated HTTP 200, bounded per-part wait, a stall after response-commit surfaces as a stream error (not clean EOF).

### D5 — Downloader-death takeover: restart from zero
Staging liveness is tied to the holder's download lock (already TTL-refreshed via `lock.StartRefresher`). If A dies, the lock expires and a waiter's existing `TryLock` takeover fires. The new owner **restarts the download from upstream from zero** (via the existing `startJob` path) rather than resuming staging mid-stream; the dead holder's partial staging parts are GC'd and `staging_state` is reset, and the new owner re-stages from zero if contention persists.

- *Why restart, not resume:* it matches the established takeover semantics (`pollForDownloadOrTakeOver` already re-downloads from scratch), and resuming would require part-integrity verification plus a byte-offset upstream resume that NAR streaming / upstream range support cannot guarantee. Reuses the lock-liveness discipline from the #1230 work rather than inventing a parallel liveness scheme.

### D6 — Flag gating and namespace
A master enable flag `--cache-inflight-staging-enabled` (env `CACHE_INFLIGHT_STAGING_ENABLED`), **default false**, plus `--cache-inflight-staging-retention` for GC grace. The feature is meaningful only with the distributed (Redis) locker; with the local locker the registry is always empty and same-pod readers already use `fileAvailableReader`, so the path is never entered. Chart key lives under `config.inflightStaging.*`, **not** `config.cdc.*` (the feature is not CDC-specific).

### D7 — GC / retention
Staging parts are reclaimed once the final representation (whole NAR or chunks, per mode) is committed **plus** `inflight-staging-retention` grace, draining in-flight tailers first. Trigger is event-driven on ingest completion, backed by a periodic sweep keyed off `staging_state.created_at` + `status` to catch holder deaths (parts whose holder died and was never taken over).

### D9 — Served compression: stage in the temp's native compression; reader transcodes at parity with the same-pod path
The same-pod reader already handles "temp holds compression X, client wants Y" (`cache.go:1307–1330`): `ds.tempFileCompression` records on-disk compression (eager-pipe temp = None; simple & non-CDC temp = `downloadURL.Compression`), and it wraps a `DecompressReader` on the fly when the client wants different bytes. Resolution: stage parts **byte-for-byte as the temp holds them**, record that compression in `staging_state.compression`, and have the part-tailing reader reuse the *identical* transcode logic. No re-compression during ingest; cross-pod serve behavior is exactly the same-pod behavior; satisfies `nar-concurrent-streaming`'s "advertise the compression you serve."

### D10 — Holder check cadence: ~1 s ticker, until first activation
A's download goroutine does not touch the DB today. Because D3 backfills from offset 0, late detection is harmless, so activation latency is non-critical. A runs a lightweight ~1 s ticker issuing one small `staging_state` read, **stopping the moment staging activates or the download ends**. Bounded, cheap, no per-byte overhead.

### D11 — No pub/sub in v1
A pub/sub wakeup would shave ~1 s of activation latency but requires plumbing a raw go-redis client into `Cache` (which holds only `lock.Locker`) and only functions with the Redis locker. Given D3 (backfill-from-0) makes latency non-critical, pub/sub is **out for v1**; it can be layered on later as a pure optimization without changing the contract.

### D12 — Helm HA validation; remove `iLoveTimeouts` (BREAKING)
`_helpers.tpl` HA guard (`replicaCount > 1`) passes when `config.cdc.enabled` **or** `config.inflightStaging.enabled`, with **no bypass**. The `config.cdc.iLoveTimeouts` value is **removed**: because staging is zero-overhead-until-contention, every HA operator can satisfy the guard safely, so the accept-the-risk escape hatch protected no remaining legitimate use case (request-affinity-routing deployments simply enable staging, which stays dormant absent contention).

- *Migration:* `cdc.iLoveTimeouts: true` → `inflightStaging.enabled: true`. Chart breaking change; CHANGELOG breaking note.
- *Scope:* `values.yaml` (drop the key), `_helpers.tpl` (guard + message), `tests/validation_test.yaml` (drop bypass cases, add neither-fails + staging-passes cases), and `nix/k8s-tests/config.nix` (the two `cdc.iLoveTimeouts = true` permutations switch to `inflightStaging.enabled`, turning "run unsafely" cases into real staging exercises).

## Risks / Trade-offs

- **Temporary storage duplication** (parts + final representation during grace) → bounded; reclaimed by D7; only under contention; documented + tunable via retention.
- **Extra DB traffic for `staging_state`** → the waiter marker is read on the `hasAsset` query it already runs every 200 ms (no new query); A adds one ~1 s `staging_state` read until activation (D10). Net is negligible versus the existing chunk-progress polling.
- **Backfill cost when a waiter arrives late** → A re-reads its own temp prefix and uploads it; bounded by NAR size, paid once per contended NAR, and only when a cross-pod reader actually exists.
- **Holder death mid-staging** → tailer stall surfaces as a stream error (not truncation); D5 restart-from-zero takeover re-drives the download; partial parts GC'd. → never a silent truncated 200.
- **Two readable representations during eager-CDC chunking** (staging parts + progressive chunks) → read-path precedence prefers staging when present (proposal `cdc-chunking` delta); progressive chunks remain the fallback.

## Migration Plan

- **Schema:** ships one additive `staging_state` table (D1) authored in `ent/schema/` and generated for all dialects (`task migrations:gen NAME=add_staging_state`), per the Ent + Atlas workflow and the expand-contract policy (new table → always safe).
- **Code:** additive and flag-gated (default off): deploying the new binary changes nothing until `inflight-staging-enabled=true`.
- **Rollback:** set the flag false (or downgrade); staging parts are reclaimed by GC, and the unused table is inert (forward-only migrations — the table is left in place).
- **Chart:** HA guard widening is backward-compatible (strictly more permissive); existing CDC-based HA installs are unaffected.

## Resolved Decisions

All open questions from the first draft are now resolved (see Decisions): waiter signal + part-progress live in a DB `staging_state` table (D1, Q1+Q3); holder polls on a ~1 s ticker until activation (D10, Q2); pub/sub is out for v1 (D11, Q4); parts are staged in the temp's native compression and transcoded at parity with the same-pod reader (D9, Q5); part size is a configurable 8 MiB default (D2a, Q6); holder-death takeover restarts the download from zero (D5, Q7).

## Open Questions

None outstanding — all resolved above. Remaining HOW-level details (exact `staging_state` column types, staging key naming, the part-tailing reader's stall-detection timeout reuse) are left to TDD in `/opsx:apply`.
