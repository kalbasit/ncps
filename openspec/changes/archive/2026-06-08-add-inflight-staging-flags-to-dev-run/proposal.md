## Why

Commit `541a25c` added the in-flight NAR staging feature (`serve --cache-inflight-staging-enabled`), the HA-safe alternative to CDC for serving a NAR cross-pod while it is still downloading (GitHub #660, #1289). The dev harness `dev-scripts/run.py` — the only supported way to spin up local single/HA instances — has no way to turn it on, so the feature cannot be exercised or reproduced locally.

## What Changes

- Add a `--inflight-staging` boolean flag to `dev-scripts/run.py`.
- When set, each spawned `serve` instance receives `--cache-inflight-staging-enabled`.
- Leave `--cache-inflight-staging-retention` (5m) and `--cache-inflight-staging-part-size` (8 MiB) at their Go defaults; do not expose them.
- Surface the flag's effective state in the startup banner and persisted `state.json` (alongside the existing `cdc`/`locker` fields) so test drivers can read it.

The upstream activation guard only engages staging when the locker is **distributed** (`--locker redis`); with `--locker local` the flag is inert. `run.py` will pass the flag as requested without adding new guard rails, matching the Go-side behavior where the feature self-disables on single-instance deployments.

## Non-goals

- Exposing `retention` or `part-size` tuning knobs (Go defaults suffice for dev).
- Changing the staging feature itself, its activation guard, or any Go code.
- Adding new dependency/guard-rail validation in `run.py` (e.g. forcing `--locker redis` when `--inflight-staging` is set).
- Documenting the production flag in `config.example.yaml` or user docs.

## Capabilities

### New Capabilities
- `dev-run-inflight-staging`: The `dev-scripts/run.py` harness exposes an `--inflight-staging` flag that propagates `--cache-inflight-staging-enabled` to each spawned `serve` instance and records its state.

### Modified Capabilities
<!-- None: no existing spec captures dev-harness flag behavior. -->

## Impact

- **Code**: `dev-scripts/run.py` only — argument parsing, the per-instance `cmd_app` assembly, the startup banner, and `state.json` (`state_config`). No Go, schema, or migration changes.
- **I/O / network / memory**: None in the harness itself. When the flag is enabled together with `--locker redis`, staging part-objects are written to shared storage during the download window per the upstream feature; this only affects runtime behavior of the spawned `ncps` processes, not `run.py`.
- **Dependencies / APIs**: None.
