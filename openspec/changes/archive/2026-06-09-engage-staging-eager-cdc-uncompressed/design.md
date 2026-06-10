## Context

In-flight staging is supposed to serve cross-pod reads during the eager-CDC chunking window, preferring staging over progressive chunks (#1355/#1374/#1375/#1379, CHANGELOG). A new e2e (`staging-contention`, chunking window) proves it does **not** engage for the uncompressed `.nar` — which predictive-`none` (#1380) makes the only request in that window. The cross-pod reader serves progressive chunks with zero contention / zero staging. Two code sites cause this:

- `cache.go:1310-1322` — the not-servable fall-through (which leads to `prePullNar` → `coordinateDownload`, the only path that records a staging request) is gated `narURL.Compression != none`. Uncompressed reads keep `hasNar == true` and serve from chunks at `:1324`, never contending.
- `cache.go:6806-6830` — poll-loop state (D): `servable` (actively chunking) + no staging parts yet → returns served-by-peer → `getNarFromChunks` → `streamProgressiveChunks`. On an early tick (before the holder's producer stages), this pre-empts staging.

`#1379`'s own body: the chunking-window staging serve is "unit-verified but has no green end-to-end run yet … needs redesigning."

## Goals / Non-Goals

**Goals:**
- An uncompressed cross-pod read during the eager-CDC chunking window, with staging enabled, contends → records a staging request → serves from in-flight staging.
- Progressive chunks remains a bounded fallback if staging parts never materialize.
- Make the `staging-contention` chunking-window e2e assert staging activation (green), and correct the CHANGELOG.

**Non-Goals:**
- Changing the staging producer, part format, GC, holder-death recovery, compressed-request handling, lazy CDC, the download window, or predictive-`none`.
- Any new flag/schema/migration (behavior-only, behind the existing default-off staging flag).

## Decisions

### Decision 1 — Site 1: route uncompressed actively-chunking cross-pod reads to coordination when staging is enabled

At `cache.go:1320`, extend the not-servable fall-through:

```go
if hasNar && !finished &&
    (narURL.Compression != nar.CompressionTypeNone || c.InflightStagingEnabled()) {
    hasNar = false
}
```

Rationale: only fires for actively-chunking (`!finished`) reads. The existing compressed case is unchanged. The new uncompressed case fires only when staging is enabled; the holder (`hasActiveLocalJob`) was already excluded at `:1303`. Staging-disabled → unchanged (progressive chunks). Alternative rejected: record the staging request inside `getNarFromChunks` — duplicates the coordination machinery and races the producer worse.

### Decision 2 — Site 2: poll-loop state (D) waits for staging parts before falling to progressive

At `cache.go:6815`, when `stagingActive` (a request was recorded at `:6703`), state (D) MUST NOT immediately return progressive. Instead keep polling (subsequent ticks re-check (C) `stagingServeReady`) until staging parts appear, the holder finishes/dies, or a **bounded** staging-wait deadline elapses — only then fall back to progressive. The bound keeps the progressive safety net for a producer that errors/stalls (consistent with the "stalled producer" requirement). Staging-disabled (`!stagingActive`) → state (D) unchanged (immediate progressive).

### Decision 3 — Flip the e2e assertion + correct CHANGELOG

`staging-contention` chunking window reverts to asserting staging activation (the `inflight-nar-staging` log line on the non-holder). The interim "chunked by exactly one replica / progressive" assertion from PR #1386 is removed. CHANGELOG: keep the staging description but attribute the *actually-working* chunking-window engagement to this change (the prior entries described intended-but-unverified behavior).

## Risks / Trade-offs

- **[State (D) wait → hang/latency]** → Mitigation: bounded staging-wait (a few 200 ms ticks) inside the existing `deadlineCtx`; falls back to progressive on timeout. Never unbounded.
- **[Truncation if staging serves an incomplete prefix]** → Mitigation: unchanged — `serveNarFromStaging` already tails to completion and the "stalled producer surfaces a stream error, not a truncated 200" requirement holds.
- **[Regressing the progressive path / staging-disabled]** → Mitigation: every change gated on `InflightStagingEnabled()`; unit tests assert the disabled path is byte-for-byte the old behavior.
- **[Same-pod / single-replica]** → Unaffected: `hasActiveLocalJob` short-circuit at `:1303` keeps the holder on its temp-file path.

## Migration Plan

Behavior-only, behind the default-off staging flag. No deploy/rollback concerns. Verified by `pkg/cache` race unit tests + `staging-contention` e2e (chunking window now activates staging) + `task fmt/lint/test`.

## Open Questions

- The exact staging-wait bound in state (D): start at ~10 ticks (2 s) and tune from the e2e (the producer stages within a tick or two once it sees the request); must stay well under the poll give-up bound.
- Whether `prefer staging over progressive chunks` (#1355, inside `getNarFromChunks`) becomes redundant once site-1/site-2 route through coordination — likely keep it as defense-in-depth; confirm no double-serve.
