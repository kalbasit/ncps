## Context

See `proposal.md` — Why, and `investigation.md` for the captured evidence.

The constraint that shapes everything here is a language-level one:

```go
// pkg/storage/local/local.go — ctx is used ONLY to open a span
_, span := tracer.Start(ctx, "local.StatNar", ...)
if _, err := os.Stat(narPath); err != nil {          // takes no context, cannot be cancelled

// pkg/storage/s3 — ctx reaches the request
if _, err := s.client.StatObject(ctx, s.bucket, key, ...); err != nil {   // cancellable
```

`os.Stat` bottoms out in `fstatat(2)`. On a `hard` NFS mount there is no way to abort it from
userspace; the goroutine — and the OS thread it is bound to — stays in the syscall until the
kernel returns. So a deadline cannot *cancel* the local probe. It can only stop the request from
*waiting* on it.

`GetNar` calls the probe synchronously at `cache.go:1307`, inside `withReadLock`, before any
status code is written. Everything downstream is therefore blocked behind it.

## Goals / Non-Goals

**Goals:**

- One bounding mechanism that works for both storage backends, without each backend
  re-implementing it.
- Genuine cancellation where the backend supports it (S3), graceful abandonment where it does not
  (local).
- Bound the resource cost of abandoned probes, so a storage brown-out degrades rather than
  exhausts the process.
- Preserve today's behaviour exactly when storage is healthy — no added latency, no new logs.

**Non-Goals:**

- Making the `local` probe cancellable. It cannot be, short of moving to `io_uring` or a
  process-isolated stat helper; both are disproportionate.
- Bounding every storage call. This change bounds the **presence probe on the NAR read path**.
  Streaming reads and writes already stream through cancellable pipes and are out of scope.
- Introducing a general-purpose storage-timeout framework.

## Decisions

### D1: Bound at the cache layer, not inside each backend

`statNarInStore` runs the backend probe on its own goroutine and selects over
`{result, deadline, ctx.Done()}`. The deadline is also pushed into the context handed to the
backend, so S3 cancels for real while local simply gets abandoned.

*Alternative — bound inside each backend:* rejected. It would duplicate the logic per backend and
still could not cancel `os.Stat`, so the local backend would need the goroutine dance anyway.

*Alternative — a dedicated stat worker pool:* rejected as premature. It becomes attractive only
if D2's cap is hit routinely, and D2 makes that measurable first.

### D2: Single-flight the probe, and cap abandoned probes

Two mechanisms bound the cost of probes nobody is waiting for:

1. **Single-flight** per `(hash, compression)`. Concurrent requests for the same NAR share one
   probe goroutine, so a stalled NAR under retry storms costs one blocked thread, not one per
   client.
2. **A cap** on simultaneously-abandoned probes. Above it, `statNarInStore` returns indeterminate
   immediately without launching another goroutine. This converts thread exhaustion into fast,
   observable degradation.

A gauge exports the number of abandoned probes in flight. This is the number to watch when
deciding whether the substrate needs fixing.

### D3: A timed-out probe is indeterminate, and indeterminate is NOT absence

The probe returns three states, not two: present, absent, indeterminate.

- **Absent** keeps today's behaviour exactly.
- **Indeterminate** routes to the existing upstream-recovery path — the request pulls from
  upstream and serves correct bytes, slower. It must never short-circuit to `404`.

*Alternative — return `503` on indeterminate:* rejected as the default. nix treats 5xx as a
substituter failure; falling back to upstream keeps builds working. A future config knob could
offer fail-fast for operators who would rather shed load than amplify upstream traffic.

**Guard (do not regress a known bug):** in **upload-only** mode, `GetNar` returns
`storage.ErrNotFound` to signal "we do not have it, please PUT it". An indeterminate probe MUST
NOT take that branch. Returning `ErrNotFound` on a *stalled* probe would make `nix copy` skip the
NAR upload and leave a phantom whose later reference check 404s — exactly the failure recorded in
`upload-reference-presence` and the phantom-nar work. Indeterminate in upload-only mode must
surface a retryable error instead. This needs an explicit test.

### D4: Configuration and defaults

A single new setting, `cache.storage.stat-timeout`, default **5s**, `0` disabling the bound
(restoring today's behaviour for rollback). 5 s is an order of magnitude above a healthy probe
(observed 8–300 ms) and an order of magnitude below the 60 s proxy read timeout, so it fires only
on genuine pathology.

### D5: Observability

- Histogram of probe duration for **all** probes, so slow-but-successful probes are visible before
  they become timeouts.
- Counter of probe timeouts, and a gauge of abandoned probes in flight.
- `warn` log on timeout carrying the NAR hash and elapsed time.
- Span event recording the timeout.

Counters must be primed with `Add(ctx, 0)` at startup — OTEL does not export an instrument until
its first increment, so an idle instance would otherwise show nothing (see
`metrics-exposure`).

## Risks / Trade-offs

- **Redundant upstream downloads while storage is stalled** → Indeterminate falls back to
  upstream, so a brown-out amplifies upstream traffic. Mitigated by single-flight (D2) and made
  visible by the timeout counter. Strictly preferable to serving truncated bytes.
- **Abandoned probes pin OS threads** → Go grows the thread pool when goroutines block in
  syscalls. Bounded by D2's cap and surfaced by the gauge.
- **Reintroducing the phantom-NAR bug** → D3's upload-only guard, with a dedicated test.
- **Masking the real problem** → This change makes ncps survive slow storage; it does not make
  storage fast. The timeout counter is the signal that the substrate still needs fixing, and the
  proposal's Non-goals say so explicitly.
- **A too-low default causing spurious fallbacks on genuinely slow-but-healthy storage** → 5 s is
  ~17× the slowest healthy probe observed; the duration histogram lets operators verify before
  tightening.

## Migration Plan

Config-only; no schema or data migration. Ships with a safe default and no behaviour change on
healthy storage. **Rollback:** set `cache.storage.stat-timeout: 0` to restore unbounded waiting,
without redeploying a different image.

## Open Questions

- Whether to expose fail-fast (`503`) as an alternative to upstream fallback on indeterminate.
  Deferrable: it is an additive config knob that changes neither the specs, this approach, nor the
  task breakdown.
