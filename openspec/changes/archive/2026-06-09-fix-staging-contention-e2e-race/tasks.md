## 1. Confirm the timing mechanism (instrumented run)

- [x] 1.1 Ran `task test:e2e CLI_ARGS='--mode local --scenario staging-contention'`; captured `var/log/ncps-850{1,2}.log` + live DB/redis state during the chunking race
- [x] 1.2 CONFIRMED (live): during the chunking race the `nar_files` row already had `total_chunks=3810` (chunked) — readers race a fully-materialized NAR. narinfo prime returns fast (handled 200 in ~1s); bg prefetch holds `download:nar` ~11s; race readers' `.nar` GETs land ~2min later. Exact source of the prime→race gap still to be revealed by in-harness timestamp logging in the validation run (Open Question #1, non-blocking)
- [x] 1.3 Signal chosen: `nar_files` row for the hash with `total_chunks == 0` = in-flight (downloading/actively chunking); `total_chunks > 0` = done/missed. Queried via `deployment.db()` (postgres). URL taken from the narinfo response (nix-base32 hand-derivation is unreliable — do NOT construct it)

## 2. Harness unit test (red)

- [x] 2.1 Added `tests/test_staging_contention.py`: `_inflight_state` (absent/inflight/done) + `_await_inflight` (race unless already chunked) classification tests
- [x] 2.2 Added `_hash_from_nar_url` tests (uncompressed / `.nar.xz` / leading-slash)
- [x] 2.3 NOTE: live runs revealed eager-CDC cross-pod reads serve from chunks, not staging → fork A. Dropped the staging-activation/bounded-retry tests; the chunking window now asserts chunk-serve correctness (integration-only)

## 3. Implement the corrected chunking-window race (green)

- [x] 3.1 `_await_inflight` reads `nar_files` via `deployment.db()` once: `total_chunks>0` → missed (logged), else race now (overlap the in-flight download)
- [x] 3.2 `_run_chunking_window` gates on `_await_inflight` then fires `_race_fetch`; no slow work between prime and race (timestamp-instrumented)
- [x] 3.3 Chunking window asserts byte-identical content + NAR chunked by exactly one replica (`migration:<hash>` on a single replica) = cross-pod served from shared chunks, no re-download/re-chunk. Removed `_race_until_activated`/bounded-retry (not needed under fork A)
- [x] 3.4 URL from narinfo response; hash via `_hash_from_nar_url`; reused `DBAccess`; download-window path keeps staging-activation assertion (`_run_download_window`)
- [x] 3.5 Existing content assertions preserved in `_assert_race_content`; 43/43 unit tests green

## 4. Verify end to end

- [x] 4.1 Ran `task test:e2e CLI_ARGS='--mode local --scenario staging-contention'`: download window PASS (staging activated [1]); chunking window PASS (all 8 readers 200, byte-identical to canonical, chunked by exactly one replica [0]). SUMMARY: 1 passed, 0 failed
- [x] 4.2 Determinism confirmed: runs #4 and #5 both PASS both windows identically (download staging [1]; chunking chunked-once [0])
- [x] 4.3 `task test:e2e:unit` green (43/43)

## 5. Finalize

- [x] 5.1 `task fmt` clean (exit 0, no changes to my files — no Python formatter in treefmt; nix/Go untouched)
- [x] 5.2 Updated `nix/e2e-tests/README.md` staging-contention description: download=staging activation, chunking=byte-identical + chunked-by-one-replica (no staging)
- [x] 5.3 `openspec validate fix-staging-contention-e2e-race --no-interactive` → valid
