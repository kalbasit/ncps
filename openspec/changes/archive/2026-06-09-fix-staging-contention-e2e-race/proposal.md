## Why

The `staging-contention` e2e scenario's **chunking window** (eager CDC) always FAILs locally with `in-flight staging did not activate`. Investigation (live runs + ncps logs) showed this is **not** an ncps bug and **not** merely a race-timing bug in the harness: under eager CDC the cross-pod reader of the uncompressed `.nar` is served *progressively from the holder's committed chunks* — the designed behavior (#1289) — and never uses in-flight staging. In-flight staging is a download-window mechanism (for the case where there are no chunks to stream). So the chunking window's assertion that "staging must activate" tests for a mechanism ncps deliberately does not use there; the window can never pass as written.

Separately, the harness raced the readers long after the eager-CDC NAR had fully materialized (the narinfo prime fire-and-forget background-prefetches and chunks the whole NAR within seconds), so the readers never even overlapped the holder's production.

## What Changes

- Split the scenario into two correctly-scoped windows:
  - **Download window (CDC off)** — unchanged in intent: a lock-losing waiter must become an in-flight staging consumer; FAIL if staging never activates.
  - **Chunking window (eager CDC)** — assert the *actual designed behavior*: race readers while the eager-CDC download+chunk is in flight, then assert every reader gets a complete byte-identical NAR AND the NAR is downloaded+chunked by **exactly one replica** (cross-pod readers served from the shared chunk set, no re-download/no re-chunk). Drop the staging-activation assertion for this window.
- Gate the chunking-window race on an observed in-flight state (a `nar_files` row with `total_chunks == 0`, or no row yet) so the readers overlap the holder's production instead of an already-materialized NAR.
- No ncps production-code change. The download-lock lifecycle, staging activation, and eager-CDC progressive chunk-serving are all already correct.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `unified-e2e-harness`: the `staging-contention` scenario requirement — the chunking window asserts byte-correct cross-pod chunk-serving with no re-download/re-chunk (chunked by exactly one replica) instead of in-flight staging activation; staging activation remains asserted only for the download window.

## Impact

- **Code**: `nix/e2e-tests/src/phases/staging_contention.py` (window split, in-flight gate, chunk-serve assertion). Harness-side only.
- **Tests**: `nix/e2e-tests/tests/test_staging_contention.py` covers the in-flight classification + URL-hash helpers under `e2e-harness-unit` (`nix flake check`); the e2e scenario itself becomes deterministic and meaningful under `--mode local`.
- **I/O / latency / memory**: none on ncps. The scenario continues to download one large NAR (gcc-unwrapped) once per window from the configured upstream; no change to ncps request handling, network, or memory.
- **Non-goals**: not changing ncps download-lock lifecycle, staging activation, or eager-CDC progressive chunk-serving; not reconciling the `inflight-nar-staging` spec's "serve from staging" vs "stream from chunks" scenarios for the chunking window (deferred — ncps implements the chunks path); not lifting the scenario's `local`-only pin to kubernetes; not touching the download-window path.
