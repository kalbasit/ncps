## Context

The `staging-contention` scenario races N clients at the same uncached NAR. The **download window** (CDC off) passes; the **chunking window** (eager CDC) always FAILed locally with `in-flight staging did not activate`.

Investigation (live runs + `var/log/ncps-*.log` + `pkg/cache/cache.go`) established, in order of discovery:

1. The download lock is already held through the eager-CDC chunking window (`<-ds.done`, `cache.go:6660`; closes post-chunk at `3304/3424`). ncps is correct; the lock-lifecycle was already fixed.
2. Under eager CDC the **narinfo prime** fires a fire-and-forget background prefetch (`prePullNar`, `cache.go:4136-4151`) that downloads+chunks the whole NAR within seconds. `GetNarInfo` returns the narinfo in ~0.1s; the prefetch holds `download:nar` ~11s. The harness then raced the readers minutes later, after materialization. Fixing this with an in-flight gate (race while a download/chunk is observable) made the readers overlap the in-flight window — confirmed live: `download:nar` held 18:51:37→49, readers fired inside it.
3. **Even with the readers racing in-flight, staging still did not activate** — because the cross-pod reader (replica B) serves the uncompressed `.nar` *progressively from the holder's committed chunks* (a DB lookup then a chunk-stream; replica B logged no upstream pull, no lock contention, no chunking). This is the designed eager-CDC behavior (#1289) and matches the `cdc-chunking` / `inflight-nar-staging` "streams from chunks" scenario. In-flight staging is a *download-window* mechanism (used when there are no chunks to stream). The chunking window's "staging must activate" assertion therefore tested the wrong mechanism.

The maintainer chose to assert the actual designed behavior for the chunking window (fork A), not to change ncps.

## Goals / Non-Goals

**Goals:**
- Download window: keep asserting in-flight staging activation (FAIL if it never activates).
- Chunking window: race readers while the eager-CDC download+chunk is in flight, then assert every reader receives a complete byte-identical NAR AND the NAR is chunked by exactly one replica (cross-pod readers served from the shared chunk set, no re-download/re-chunk).
- Harness-only; no ncps production change.

**Non-Goals:**
- Changing ncps download-lock lifecycle, staging activation, or eager-CDC progressive chunk-serving.
- Reconciling the `inflight-nar-staging` "serve from staging" vs "stream from chunks" scenarios for the chunking window (ncps implements chunks; spec cleanup deferred).
- Lifting the `local`-only pin; touching the download-window path.

## Decisions

### Decision 1 — Chunking window asserts progressive chunk-serving, not staging

The correctness property for the eager-CDC chunking window is: contending cross-pod readers get the complete, byte-identical NAR **without re-downloading or re-chunking** — they serve from the holder's shared chunk set. Staging activation is asserted only in the download window (no chunks to stream there). Rejected: keep asserting staging in the chunking window — ncps deliberately serves from chunks there, so it could never pass.

### Decision 2 — "No re-download/re-chunk" via the per-hash chunking lock

The assertion uses `_scan_logs(deployment, f"migration:{hash}")` and requires exactly one replica. The per-hash migration (chunking) lock is taken only by the replica that actually chunks the NAR; a cross-pod re-download would re-chunk and appear as a second replica. Empirically (run logs) the lock appears on the holder only (8501=present, 8502=0), and the cross-pod's progressive serve never touches it. Rejected alternatives: counting `download:nar` acquisitions (a coordinating reader may acquire the freed lock without re-downloading — false positive); scanning the upstream-pull log (less hash-specific).

### Decision 3 — Best-effort in-flight gate, no retry

`_await_inflight` reads the `nar_files` row once: `total_chunks > 0` → `missed` (already materialized, logged as a note), else `inflight` (race now). We race immediately after the 0.1s prime so readers overlap the long download phase. Because the correctness assertions (byte-identical + chunked-once) hold whether the readers race mid-production or against the committed chunk set, no bounded retry / clean-restart loop is needed — it was removed.

### Decision 4 — Harness unit test for the pure helpers

`_inflight_state`, `_await_inflight`, and `_hash_from_nar_url` are covered by `tests/test_staging_contention.py` with a scripted fake DB, under `e2e-harness-unit` in `nix flake check`. The race + chunk-serve assertions are integration-only (exercised by the e2e run).

## Risks / Trade-offs

- **[`migration:<hash>` also touched by a non-chunking replica]** → Mitigation: empirically holder-only; the progressive chunk-serve path does not acquire the per-hash migration lock. If a future ncps change makes a reader take it, the assertion would over-count — revisit with a writer-specific needle (`recordChunkBatch`) then.
- **[Readers race after materialization (gate "missed")]** → Accepted: the byte-identical + chunked-once assertions still hold; the gate is best-effort and only logs the case. The download phase (~10s for gcc) makes a mid-production race the common case.
- **[Spec tension between staging vs chunks scenarios]** → Out of scope: documented as a deferred `inflight-nar-staging` spec reconciliation; ncps implements the chunks path and this harness asserts it.

## Migration Plan

Pure test-harness change. No deploy/runtime impact, no rollback concerns. Verified by `nix run .#e2e -- --mode local --scenario staging-contention` green (both windows) and `nix flake check` (`e2e-harness-unit`).

## Open Questions

- **Resolved — cause of the original late firing**: the narinfo prime returns in ~0.1s but the harness raced minutes later; the in-flight gate now fires the race immediately after prime, confirmed overlapping the held `download:nar` window.
- **Resolved — why staging never activated**: eager-CDC uncompressed cross-pod reads are served progressively from chunks, not staging.
- **Deferred (ncps/spec)**: whether the `inflight-nar-staging` spec's "reader contends and serves from staging mid-chunking" scenario should be reconciled with the "streams from chunks" scenario. ncps implements chunks; this is a spec-cleanup item, not part of this harness change.
