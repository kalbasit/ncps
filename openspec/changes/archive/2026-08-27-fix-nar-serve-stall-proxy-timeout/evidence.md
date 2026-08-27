# Measured effect of the fix

Task 7.4. Numbers, not assertions. `investigation.md` covers how the defect was found;
this records what the fix changed.

## Production baseline (the defect, v0.10.0-rc17)

Measured against the two prod replicas via `kubectl port-forward`, bypassing nginx so the
number is ncps's own time-to-first-byte. Sequence: request the narinfo on replica A (A pulls
and stores the NAR), then request the NAR on replica B.

| package | NAR size | TTFB on the non-holder replica |
| --- | ---: | ---: |
| blender | 108,662,723 B | **107.5 s** |
| openjdk17 | 349,188,671 B | **57.4 s** |
| gcc14 | **12,524 B** | **56.8 s** |
| wine64 | 66,189,221 B | **57.8 s** |
| godot_4 | 69,967,331 B | **57.5 s** |

A 12 KB NAR at 56.8 s rules out size and bandwidth. ncps's own request log agreed
(`elapsed: 56800.09 ms`). Healthy reads on the same pods: **8–300 ms**.

Root cause, from a goroutine dump captured mid-stall (`goroutine-stall-dump.txt`): one
`os.Stat` → `fstatat(2)` blocked ~57 s inside `statNarInStore`, with the pod otherwise idle
(81 goroutines, exactly one in `[syscall]`). The mount is `hard,timeo=600,retrans=2` —
`timeo` in deciseconds, so 60 s per RPC cycle, up to 2 retries. The observed 56.8–57.8 s and
107.5 s are one cycle and two. Exact fit.

## Unit-level: before vs after

`TestGetNarBoundedTimeToFirstByte` — a `StatNar` that blocks 30 s and ignores context
cancellation (modelling `os.Stat`), against a 250 ms configured bound.

| | result |
| --- | --- |
| **Before** (field present but inert) | `GetNar` never returned; the test failed after exhausting its **10 s** budget |
| **After** | `GetNar` resolved in **1.00 s** |

Verbatim RED output before the fix:

```
--- FAIL: TestGetNarBoundedTimeToFirstByte (10.01s)
    GetNar did not resolve within 10s while the storage probe blocked for 30s:
    a NAR request must have a bounded time-to-first-byte
```

## Contention: probes collapsed

`TestStatProbeIsSingleFlighted` — 20 concurrent callers for the same NAR against a stalled
store:

```
20 concurrent callers produced 1 backend probe(s)
```

Without this, a stalled NAR under a client retry storm costs one blocked goroutine — and on
the local backend one pinned OS thread — per client.

## No regression when storage is healthy

- `TestStatNarInStoreFastProbeUnaffected`: a healthy probe returns the same
  present/absent answers with no timeout error and no added latency.
- e2e `single-local-sqlite`, warm NAR re-read: **ttfb = 0.001 s** against a 15 s budget, PASS.
- Full unit suite (`task test`): exit 0.

## Regression coverage added

| test | pins |
| --- | --- |
| `TestGetNarBoundedTimeToFirstByte` | bounded time-to-first-byte |
| `TestStatNarInStoreTimeoutIsIndeterminate` | a timed-out probe is undetermined, not absent |
| `TestUploadOnlyIndeterminateIsNotNotFound` | a stall never becomes `ErrNotFound` (phantom-NAR guard) |
| `TestStatProbeDeadlineReachesBackend` | the deadline reaches the backend's context |
| `TestStatProbeIsSingleFlighted` | concurrent probes collapse |
| `TestStatNarInStoreFastProbeUnaffected` | healthy storage is unaffected |
| e2e serve phase | warm-read TTFB budget on every serving scenario |
| `tests/test_client_ttfb.py` (4) | the harness can tell byte-correct-but-slow from fast |

## Request-level budget (found in review, PR #1479)

CodeRabbit flagged that a request could "spend multiple probe timeouts". Verified: it
could. Bounding each probe individually was not enough, because one `GetNar` consults the
store several times.

| | `GetNar` elapsed against a 300 ms bound |
| --- | --- |
| Per-probe bound only | **1.20 s — 4.0x** |
| With a cumulative per-request budget | **0.30 s — 1.0x** |

At the 5 s default that was 20 s rather than 5 s; at a 15 s setting it would have put a
request back over a 60 s proxy read timeout — reintroducing the exact failure this change
fixes. Pinned by `TestRequestProbeBudgetIsCumulative`.

The budget is scoped to probes only. It deliberately does not bound the download, which
legitimately takes far longer than any probe should.

## What this does NOT fix

The substrate. `ncps_storage_stat_timeout_total` is the signal: if it is non-zero in
production, storage is still stalling and the fix is converting those stalls into upstream
fallbacks rather than truncated responses. The NFS mount tuning and the move to the S3
backend are tracked as infrastructure work, out of scope here (see `proposal.md` — Non-goals).
