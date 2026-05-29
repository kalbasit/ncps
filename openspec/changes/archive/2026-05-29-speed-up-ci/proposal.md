# Proposal: speed-up-ci

## Why

CI averages ~20 min and routinely exceeds 30, failing roughly half of runs so a
PR takes several attempts to merge. Measured cause (run 26653285682): the
shared `build` job fans out over a 2-architecture matrix and runs the **entire**
`nix flake check` — all integration cohorts (Garage/S3, Postgres, MySQL, Redis)
— on **both** `x86_64-linux` and `aarch64-linux`. The aarch64 leg runs on slower
ARM runners where the Postgres cohort times out, making it both the long pole
(25.7 min vs 17.4 min) and the dominant failure. Integration tests are
architecture-independent, so the aarch64 run buys no extra signal — only wall
clock and flakiness.

## What Changes

- The integration `nix flake check` (and the coverage build/upload that depends
  on it) runs on a **single designated architecture** (`x86_64-linux`) only.
- The `aarch64-linux` leg keeps building and pushing its OCI image — the one
  genuinely architecture-specific artifact — so the multi-arch manifest is
  unaffected. It no longer re-runs the integration cohorts.
- This is gated behind a new opt-in input on the shared `build.yml` workflow
  (in `kalbasit/gh-actions`); ncps's CI caller sets it. Other consumers keep
  today's run-everywhere behavior by default. **(cross-repo change)**

## Capabilities

### New Capabilities
- None.

### Modified Capabilities
- `flake-check-topology` (ncps): add a requirement that, in CI, the integration
  cohort suite and coverage are validated on one canonical architecture, while
  non-canonical architectures only build the deployable image.
- `oci-build` (kalbasit/gh-actions): the build workflow gains an input to scope
  the `nix flake check` + coverage steps to a subset of matrix systems, while
  still building/pushing the OCI image for every system. Tracked as a sibling
  OpenSpec change in the gh-actions repo.

## Impact

- **Affected systems:** `kalbasit/gh-actions` `build.yml` (`oci-build`) and its
  spec; ncps `.github/workflows/ci.yml` caller; ncps `flake-check-topology`
  spec. No production/runtime Go code changes.
- **CI wall clock:** removes the ~26 min flaky aarch64 integration leg from the
  critical path; expected PR CI ≈ the x86 build time (~17 min today) minus the
  removed duplication, and elimination of the recurring Postgres-on-aarch64
  reruns.
- **I/O / network / memory:** no change to ncps runtime I/O, network latency, or
  memory. CI-only: fewer ARM runner-minutes and one fewer integration DB spin-up
  per run.
- **Coverage:** unchanged — coverage already derives from the x86 cohort run.

## Non-goals

- Re-architecting the dd9622c per-backend cohort split (the 5× recompile that
  took flake-check ~8m→~14m). That is a separate slowness lever, deferred.
- Dropping aarch64 from CI entirely; the image is still built and published per
  architecture.
- Changing which backends or tests run, the race detector, or local
  `nix flake check` behavior (still checks all systems locally).
