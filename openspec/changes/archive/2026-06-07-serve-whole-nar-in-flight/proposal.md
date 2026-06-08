## Why

While a replica is ingesting a NAR, the only complete copy is a node-local temp file (`c.tempDir` → `fileAvailableReader`), invisible to every other replica. On the S3 backend there is **no way to read an in-progress object**, so a second replica requesting the same NAR mid-download has nothing to stream: it waits past client timeouts (#660) or, in eager-CDC, falls back to the fragile `streamProgressiveChunks` path that can reassemble a truncated NAR (#1289). CDC (v0.9.0) only moved the fragility from the download window into the chunking window; the cross-pod fast path was never built. This is one root cause at two stages of one ingest pipeline, and it is **CDC-mode-independent** — it occurs under non-CDC, lazy-CDC, and eager-CDC alike.

## What Changes

Introduce a **contention-activated in-flight staging copy** of the NAR in shared storage, written as cheap sequential immutable part-objects (fixed-size; **no** content-defined hashing or dedup, so lazy-CDC's CPU savings on slow machines are preserved).

- **Gated by an explicit enable flag, OFF by default** (`--cache-inflight-staging-enabled` / chart `config.inflightStaging.enabled`). Operators not running HA leave it off and pay nothing — the code path is never entered.
- **When enabled, the default single-reader path is still unchanged**: a NAR with no concurrent cross-pod reader downloads to temp and stores normally — **zero staging overhead** until contention.
- **Activation on contention**: when a cross-pod waiter is detected for an in-flight NAR (lock held by another replica via Redis), the owning replica backfills the already-downloaded prefix from its temp file as part-objects and continues appending parts as bytes arrive. Waiters **tail completed parts** to reassemble a complete, byte-correct NAR — covering the active-download window for **all** CDC modes.
- **Lock-losing waiters never return HTTP 500**: they serve from staging when active, otherwise a correct **bounded-wait + clean handoff** (subsumes the in-progress `fix-download-coordination-500`).
- **Subsumes #1289**: the same staging parts, kept alive through the eager-CDC chunking window plus a grace period, *are* the "whole NAR in shared storage" the read path prefers over `streamProgressiveChunks` — one mechanism, not three.
- **Background GC** reclaims staging parts after the final representation is committed plus a configurable grace period (`--cache-inflight-staging-retention`), draining in-flight readers first.
- **Helm HA validation: CDC is no longer the only HA-safe option.** The chart's `replicaCount > 1` guard (`_helpers.tpl`, today "requires CDC or `iLoveTimeouts`") MUST pass when **either** `config.cdc.enabled` **or** `config.inflightStaging.enabled` is set. **BREAKING:** the `config.cdc.iLoveTimeouts` bypass is **removed** — since staging is zero-overhead-until-contention, every HA operator can satisfy the guard safely, so the accept-the-risk escape hatch is obsolete. Migration: set `config.inflightStaging.enabled=true`. Updated error message and `validation_test.yaml` cases.
- **Documentation** under `docs/docs` (HA deployment guidance, the new flag, the CDC-or-staging-for-HA choice, Chart Reference row) and a **`CHANGELOG.md`** `[Unreleased]` entry.
- Resolves **#660** (active-download window, all modes) and **#1289** (eager-CDC chunking window).

## Capabilities

### New Capabilities
- `inflight-nar-staging`: the enable flag (off by default), contention-activated staging of an in-flight NAR to shared storage as sequential part-objects, cross-pod progressive tailing, read-path precedence, and retention-gated GC.
- `helm-ha-staging-validation`: the chart's HA (`replicaCount > 1`) guard is satisfied by CDC **or** in-flight staging, with no bypass (`iLoveTimeouts` is removed).

### Modified Capabilities
- `nar-concurrent-streaming`: a lock-losing replica MUST serve from in-flight staging when active, else bounded-wait + clean handoff — never HTTP 500; cross-pod progressive reads MUST reassemble a complete, non-truncated NAR.
- `cdc-chunking`: during the eager-CDC chunking window (`total_chunks == 0`), the read path MUST prefer present staging parts over `streamProgressiveChunks`.

## Non-goals

- **Not** always-on: the feature is flag-gated (off by default) and, even when enabled, staging activates only under real cross-pod contention; single-reader and slow-machine lazy downloads pay nothing.
- No deduplication, no change to the CDC chunking algorithm, chunk-store layout, or `migrate-chunks-to-nar` drain/migration flows.
- Not the crash-mid-chunking orphan-recovery fix (#1230); steady-state serve-during-ingest only.
- Request-affinity routing is **out of scope** as a dependency; it remains an optional, documented deployment optimization, not a correctness mechanism.

## Impact

- **Code**: `pkg/cache/cache.go` (contention detection, staging writer/backfill, tailing reader, lock-loss handoff, read-path precedence, GC), `pkg/storage` `NarStore` part-object scheme, cross-pod waiter signalling (Redis), new enable + retention flags in `cmd/`.
- **Helm chart**: `values.yaml` (add `config.inflightStaging.*`, **remove `config.cdc.iLoveTimeouts`**), `_helpers.tpl` HA validation (CDC **or** staging, no bypass), `validation_test.yaml`, `tests/` coverage, `nix/k8s-tests/config.nix` permutations (switch `iLoveTimeouts` perms to `inflightStaging.enabled`); CHANGELOG carries a **BREAKING** removal note.
- **Docs**: `docs/docs` HA/deployment guidance + flag reference + Chart Reference row; **`CHANGELOG.md`** `[Unreleased]` entry.
- **I/O / network**: extra shared-storage writes **only under contention**; bounded, retention-reclaimed duplication. Eliminates per-chunk DB queries + chunk-store round-trips + 200 ms poll cycles on the contended cross-pod path.
- **Memory**: no whole-NAR buffering; parts are streamed, not held. Same-pod fast-path latency unaffected.
- **Specs**: new `inflight-nar-staging`, `helm-ha-staging-validation`; deltas to `nar-concurrent-streaming` and `cdc-chunking`.
