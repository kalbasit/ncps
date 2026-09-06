## 1. RED: prove the defect before changing behaviour

- [x] 1.1 Add a `slowStore` test double wrapping a `NarStore` whose `StatNar` blocks for a
      configurable duration (and optionally ignores context, mimicking `os.Stat`); verify with a
      direct unit test that the double blocks and unblocks as configured.
- [x] 1.2 Write a RED test asserting `GetNar` returns a first byte or an error within a short
      budget while `slowStore.StatNar` blocks far longer; verify it FAILS against current `main`
      with a timeout, and record the observed failure in the commit message.
- [x] 1.3 Write a RED test asserting a timed-out probe is classified indeterminate and does NOT
      produce `404`; verify it fails today because a stalled probe never returns a classification
      at all.
- [x] 1.4 Write a RED test asserting that in **upload-only** mode an indeterminate probe does NOT
      return `storage.ErrNotFound` (design D3 guard, prevents re-introducing the phantom-NAR
      bug); verify it fails today.

## 2. Configuration

- [x] 2.1 Add `cache.storage.stat-timeout` (flag + env + YAML, default `5s`, `0` disables)
      threaded to the cache; verify with a config unit test covering default, override and the
      disabling `0` value.
- [x] 2.2 Document the new setting in `config.example.yaml` with the rationale for 5s; verify by
      loading the example config in the existing config test.

## 3. GREEN: bound the storage presence probe

- [x] 3.1 Introduce a three-state probe result (present / absent / indeterminate) at the
      `statNarInStore` boundary, leaving today's two states behaviourally identical; verify
      existing cache tests still pass unchanged.
- [x] 3.2 Run the backend probe on its own goroutine in `statNarInStore`, selecting over
      result / deadline / `ctx.Done()` (design D1); verify test 1.2 turns GREEN.
- [x] 3.3 Propagate the deadline into the context passed to the backend so the S3 backend
      genuinely cancels; verify the backend receives a deadline-carrying context and is cancelled
      at it. NOTE: asserted against a context-recording store double rather than a live S3 bucket
      — the property owned by this codebase is that ncps *supplies* a cancellable context; whether
      MinIO/Garage honours it is their contract and would need live infrastructure to assert.
- [x] 3.4 Wire `GetNar` to route indeterminate to the existing upstream-recovery path, never to
      `404`; verify tests 1.3 and 1.4 turn GREEN.

## 4. Bound the cost of abandoned probes

- [x] 4.1 Single-flight the probe per `(hash, compression)` so concurrent requests for the same
      NAR share one probe goroutine; verify with a test that N concurrent `GetNar` calls against a
      stalled store create exactly one backend probe.
- [x] 4.2 Cap simultaneously-abandoned probes, returning indeterminate immediately once exhausted
      (design D2); verify with a test that the cap is respected and no additional goroutines are
      launched beyond it.

## 5. Observability

- [x] 5.1 Record a probe-duration histogram for all probes plus a timeout counter and an
      in-flight-abandoned gauge, priming counters with `Add(ctx, 0)` at startup; verify the
      instruments appear on `/metrics` on an idle instance.
- [x] 5.2 Emit a `warn` log with NAR hash and elapsed time, and a span event, on probe timeout;
      verify with a test asserting the log record is produced on timeout and absent on a fast
      probe.

## 6. e2e: assert time-to-first-byte, not just bytes

- [x] 6.1 Add time-to-first-byte measurement to the harness NAR client (first-byte timestamp
      distinct from full-body completion); verify with a harness pytest against a stub server that
      delays its first byte.
- [x] 6.2 Assert TTFB against a declared budget on a warm NAR read, with a client timeout
      strictly larger than the budget so a stall reports a measured violation rather than an
      opaque timeout. NOTE: implemented in the shared `serve` phase rather than as a separate
      catalog entry, so every serving scenario carries the assertion. Verified by running
      `--mode local --scenario single-local-sqlite`: `warm NAR ttfb=0.001s (budget 15.0s)`, PASS.
- [x] 6.3 Cover the TTFB measurement in the `e2e-harness-unit` flake check. NOTE: no
      `config.nix` entry is needed — the assertion lives in the shared `serve` phase, so the
      existing catalog entries all exercise it. Verified: `nix build .#checks.x86_64-linux.e2e-harness-unit`
      passes, 70 tests including the 4 new TTFB tests.

## 7. Verification and close-out

- [x] 7.1 Confirm the four RED tests from group 1 are GREEN and re-run the full unit suite;
      verify `task test` exits zero.
- [x] 7.2 Run `task fmt` and `task lint`; verify both exit zero.
- [x] 7.3 Run `openspec validate fix-nar-serve-stall-proxy-timeout --no-interactive --strict`;
      verify it reports the change valid.
- [x] 7.4 Record in the change directory the measured TTFB under a simulated stall (before/after),
      so the fix's effect is evidenced rather than asserted.
