## Context

`dev-scripts/run.py` is the supported local harness for ncps. It parses backend choices (`--db`, `--storage`, `--locker`, `--replicas`), runs migrations, then assembles a `serve` command line per instance in `cmd_app` and persists run state to `var/ncps/state.json`. It already has a parallel pattern for an opt-in feature flag: `--enable-cdc` / `--enable-lazy-cdc` append `--cache-cdc-*` args and `cdc` is recorded in `state_config`.

Commit `541a25c` added `serve --cache-inflight-staging-enabled` (plus `-retention`/`-part-size`), whose activation guard requires a distributed locker. `run.py` has no way to set it, so the feature is unreachable locally. This change adds the toggle following the existing CDC-flag pattern.

## Goals / Non-Goals

**Goals:**
- Add `--inflight-staging` to `run.py` that emits `--cache-inflight-staging-enabled` to every spawned instance.
- Record the state in the startup banner and `state.json` for test drivers.
- Reuse the established `--enable-cdc` flag/banner/state conventions for consistency.

**Non-Goals:**
- Exposing `retention` / `part-size` knobs (Go defaults are fine for dev).
- Adding locker guard rails in `run.py`; the Go guard already self-disables on non-distributed lockers.
- Any Go, schema, migration, or docs change.

## Decisions

- **Flag name `--inflight-staging` (not `--enable-inflight-staging`).** The proposal scopes a single toggle. The existing CDC flags use an `--enable-` prefix, but the simpler `--inflight-staging` reads cleanly and there is only one staging flag to add. Alternative considered: mirror `--enable-cdc` naming exactly — rejected as marginally more verbose with no benefit, but either is acceptable and the implementer may match the CDC prefix if preferred for symmetry.
- **Emit only when set; never pass retention/part-size.** Keeps the harness honest about the Go defaults and avoids drift if those defaults change upstream. Alternative: always pass all three with hardcoded values — rejected (couples the harness to upstream default values).
- **No guard rails.** Pass the flag through regardless of `--locker`. Mirrors the Go guard (`InflightStagingEnabled()` is true only with a distributed locker), so `--inflight-staging --locker local` is harmless and non-erroring. Alternative: error or warn when locker is local — rejected as out of scope and redundant with the Go-side guard; a warning could be added later if it proves confusing.
- **State key `inflight_staging` in `state_config`.** Sits next to `cdc`/`locker`. Boolean mirrors `args.inflight_staging`.

## Risks / Trade-offs

- **[Flag is silently inert with `--locker local`]** → Documented in the spec scenario and surfaced in the banner so the user sees the enabled/disabled state; the Go guard is the source of truth.
- **[Banner/state drift if future staging sub-flags are added]** → Out of scope now; the single boolean is trivially extendable later.

## Migration Plan

Not applicable — additive change to a dev-only script, no persisted schema or production surface. Rollback is reverting the diff.

## Open Questions

- None. Naming (`--inflight-staging` vs `--enable-inflight-staging`) is the only soft choice and is left to the implementer per the Decisions note.
