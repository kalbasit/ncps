# Design: speed-up-ci

## Context

ncps CI is a thin caller of `kalbasit/gh-actions`'s reusable orchestrator
(`.github/workflows/ci.yml@main`). With `oci: true`, the orchestrator's `build`
job calls `build.yml`, which fans out a matrix over `inputs.systems`
(`["x86_64-linux","aarch64-linux"]`) and, **on every leg**, runs sequentially:
`nix flake check -L` → build coverage → upload to Codecov → build/push OCI image.

The aarch64 leg runs on `ubuntu-24.04-arm`. Measured on run 26653285682:
x86 build = 17.4 min (pass), aarch64 build = 25.7 min (**fail** — Postgres
cohort times out). The integration cohorts (Garage/Postgres/MySQL/Redis) are
architecture-independent, so the aarch64 run produces no signal the x86 run
doesn't — it only adds wall-clock and a recurring flaky failure that forces
PR reruns.

The repo `kalbasit/gh-actions` is shared by other consumers (e.g.
signal-api-receiver), so behavior changes there must be opt-in and default to
today's behavior.

## Goals / Non-Goals

**Goals**
- Remove the integration test suite (`nix flake check`) + coverage from the
  aarch64 CI leg; keep it on `x86_64-linux`.
- Keep building and pushing the aarch64 OCI image so the multi-arch manifest is
  unchanged.
- Make the gh-actions behavior opt-in; other consumers keep run-everywhere.

**Non-Goals**
- Reworking the dd9622c per-backend cohort split (the ~8m→~14m flake-check
  regression). Deferred.
- Removing aarch64 from CI entirely.
- Any ncps runtime/Go code change.

## Decisions

### D1 — New `test_systems` input on the shared workflows (opt-in subset)
Add a `test_systems` input (JSON-array string, default `"[]"`) to both
`build.yml` and the orchestrator `ci.yml` in gh-actions. Semantics: empty array
⇒ run flake-check/coverage on **all** `systems` (preserves current behavior for
other consumers). Non-empty ⇒ run those steps only on legs whose
`matrix.system` is in the list. ncps passes `test_systems: '["x86_64-linux"]'`.

Gating expression on the relevant steps:
`inputs.test_systems == '[]' || contains(fromJson(inputs.test_systems), matrix.system)`.

Steps gated: **Flake check**, **Build coverage**, **Upload coverage to Codecov**.
Steps that stay on every arch: registry validation, version.txt, metadata,
**Build the OCI image** (`nix build .#packages.<system>.docker`), pusher, login,
push, and the `oci-manifest` job.

*Alternatives considered:*
- *Hardcode flake-check to `x86_64-linux` in build.yml* — rejected: not generic,
  breaks other consumers and aarch64-only builds.
- *A boolean `skip_aarch64_tests`* — rejected: architecture-specific, less
  expressive than naming the canonical test system(s).
- *Drop aarch64 from `systems` on PRs only* — rejected by scope choice; we still
  want the aarch64 image built/tested-for-build on PRs.

### D2 — OCI image build is independent of flake-check
`nix build .#packages.<system>.docker` builds the lean `packages.ncps` (no
tests; see `flake-check-topology` "Main package build performs no tests"). It
does not depend on the cohort checks, so skipping flake-check on aarch64 leaves
a valid, pushable aarch64 image. Coverage/Codecov already only feed the x86
profile (one merged `coverage.txt`), so dropping them on aarch64 changes
nothing about reported coverage.

### D3 — Plumb through the orchestrator, not around it
ncps calls the orchestrator `ci.yml`, which itself calls `build.yml`. Both must
learn `test_systems`: ncps caller → orchestrator `ci.yml` → `build.yml`. The
orchestrator's standalone `flake-check` job (used when `oci: false`) gets the
same gating for consistency, governed by the `reusable-ci` spec.

## Risks / Trade-offs

- **aarch64-specific test failures go uncaught until a native run.** → Accepted:
  the suite is integration/DB-focused and arch-independent; the aarch64 image is
  still *built* every run, catching compile/build breaks. Arch-specific runtime
  bugs were already not the failure mode we see.
- **On push-to-main, a failing x86 flake-check still lets the aarch64 image push
  before the gate fails.** → Pre-existing (legs push independently today); not
  worsened. Noted, not addressed here.
- **gh-actions change affects other consumers.** → Mitigated by default `"[]"`
  = current behavior; only ncps opts in.

## Migration Plan

1. Land the gh-actions change (build.yml + ci.yml + both spec deltas) first;
   tag/`@main` is consumed by ncps.
2. Land the ncps change (`ci.yml` sets `test_systems`, spec delta) — it depends
   on the gh-actions input existing.
3. Verify on a PR: aarch64 build leg no longer runs cohorts; wall-clock drops;
   multi-arch manifest still assembles.
4. **Rollback:** remove `test_systems` from the ncps caller (reverts to
   run-everywhere); gh-actions input is inert when unused.

## Open Questions

- Should the orchestrator default `test_systems` to the runner-native arch
  automatically (so every consumer benefits without opting in)? Deferred —
  starting opt-in is safer for shared consumers.
