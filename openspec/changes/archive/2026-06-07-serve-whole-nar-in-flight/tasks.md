# Tasks

> All production changes follow TDD (`/tdd`): write the failing test first, then implement.
> Run `task fmt`, `task lint`, `task test` (and `task ent:check`) before considering any group done.

## Execution workflow (stacked PRs — READ FIRST)

This change ships as a **git-spice stack: one branch/PR per task-group**, to keep each
group's blast radius isolated and reviewable. Follow this exactly:

1. **`openspec/` is committed ONLY in the final archive branch (group 14 below).** Never
   stage or commit `openspec/` in any group branch — it stays untracked in the working tree
   and rides along across branch switches. This keeps every intermediate PR free of an active
   `openspec/changes/` dir so the CI openspec-guard gate passes (it blocks merge on any active
   change). Update the checkboxes in this file as you go; they get committed in the final branch.
2. **One group = one stacked branch.** Implement the group under TDD, stage ONLY that group's
   code/test/docs files (explicitly `git add <paths>`, never `git add -A`), then run `/gs-create`
   (`gs branch create -am "type(scope): ..."`) to commit it onto a new branch on top of the stack.
3. **Verify before each `/gs-create`:** `task fmt`, `task lint`, `task test` (+ `task ent:check`
   when ent/migrations changed) must pass. The pre-commit hooks also run golangci-lint.
4. **Do NOT `git push` / `gs submit`** — only the user submits the stack.
5. **Final branch (group 14):** run `/opsx:verify` then `/opsx:archive` (archives + syncs the
   delta specs into `openspec/specs/` and moves the change to `openspec/changes/archive/`), then
   commit the resulting `openspec/` changes as the top branch of the stack.

### Current state (resume point)
- **Groups 1–3 (foundation)** committed on branch `user/wnasreddine/fix-issue-1289` (commit
  `7c708c38`) — NOT a new gs branch; it is the stack base.
- **Group 4** committed on gs branch `feat-storage-in-flight-staging` (top of stack).
- **Start here: group 5.** Each of groups 5–13 gets its own `/gs-create` branch; then group 14
  is the openspec archive branch.
- Dev backing services (PG/MySQL/Redis/Garage, fixed ports) may need restarting:
  `nix run .#deps -- --detached --tui=false` (plain `nix run .#deps` fails — no TTY).

## 1. Configuration flags

- [x] 1.1 Add `--cache-inflight-staging-enabled` (env `CACHE_INFLIGHT_STAGING_ENABLED`, default `false`) to `cmd/` flag set and `config.example.yaml`.
- [x] 1.2 Add `--cache-inflight-staging-retention` (duration, grace before GC) and `--cache-inflight-staging-part-size` (default 8 MiB) flags + config keys.
- [x] 1.3 Thread the three settings into `Cache` construction (new fields + setters); add a guard helper `inflightStagingEnabled()` that is also false when the locker is non-distributed (local).
- [x] 1.4 Test: flags parse, defaults are correct, and `inflightStagingEnabled()` is false under the local locker even when the flag is true.

## 2. `staging_state` Ent schema + migration

- [x] 2.1 Write `ent/schema/staging_state.go`: keyed by NAR `hash`, columns `requested_at` (nullable), `parts_available` (int, default 0), `compression` (string), `status` (plain string requested/staging/complete/abandoned — chosen over `field.Enum` to stay dialect-portable and avoid the Postgres-enum invariant), `created_at` (via Timestamps mixin). Table-level CHECK `parts_available >= 0`; unique index on `hash`; index on `created_at` for the GC sweep.
- [x] 2.2 Run `task ent:generate` then `task ent:lint`; confirm zero invariant violations.
- [x] 2.3 Generate migrations: `task migrations:gen NAME=add_staging_state` (shared prefix `20260607182925` across dialects); `task ent:check` exits 0.
- [x] 2.4 Test: `go test ./migrations/...` green; cross-dialect schema-equivalence covered by `nix flake check` in group 13.

## 3. `staging_state` data access

- [x] 3.1 Test: a waiter can insert/mark a "staging requested" record for a hash with no pre-existing `nar_file` row.
- [x] 3.2 Implement create-or-mark-requested, advance-parts-available, set-status, read-by-hash, and reset (for takeover) using the Ent client (`pkg/cache/inflight_staging.go`).
- [x] 3.3 Test: idempotent mark-requested collapses to one row (`OnConflictColumns(hash).Ignore()` = DB-level ON CONFLICT DO NOTHING, safe under concurrency).

## 4. Staging part-object storage (`NarStore`)

- [x] 4.1 Test: write ordered fixed-size part-objects under a staging key namespace for a hash and read them back as a contiguous stream (`pkg/storage/local/staging_part_test.go`).
- [x] 4.2 Implement `PutStagingPart` / `GetStagingPart` / `DeleteStagingParts` on the `NarStore` interface and both `local/` (atomic temp+rename, `<root>/staging/<hash>/<index>.part`) and `s3/` (`<prefix>/store/staging/<hash>/<index>.part`) backends.
- [x] 4.3 Test: a missing/uncommitted part reads as `storage.ErrNotFound`; local atomic rename guarantees no partial part is visible. (s3 contiguous read exercised by the group-13 e2e.)

## 5. Holder side — contention detection, activation, backfill

- [x] 5.1 Test: with staging enabled and a `staging_state` request present, the download holder begins staging; with no request, it does not (zero overhead).
- [x] 5.2 Implement a ~1 s ticker in the download goroutine that reads `staging_state` until staging activates or the download ends (D10).
- [x] 5.3 Test: on activation mid-download, the holder backfills the already-written temp prefix as parts from index 0, then appends new parts; `parts_available` advances only after each part is durable.
- [x] 5.4 Implement backfill-from-zero + append, recording `compression` from `ds.tempFileCompression` (D9) and advancing `parts_available`.
- [x] 5.5 Test: parts cover the full NAR byte range exactly once (no gap/overlap), across all four download paths (eager-pipe, eager-simple, lazy, non-CDC). [producer is download-path-agnostic — it tails the temp file; full e2e path coverage in group 13]

## 6. Reader side — waiter tails staging parts

- [x] 6.1 Test: a lock-losing waiter with `parts_available > 0` serves the complete, byte-correct NAR by tailing parts (HTTP 200), in non-CDC mode. [reader byte-correctness + EOF unit-tested; full cross-pod HTTP serve in group 13 e2e — local locker blocks rather than failing, so lock-loss is a Redis-only path]
- [x] 6.2 Implement a part-tailing reader (analogous to `fileAvailableReader`) over part-objects + the `parts_available` marker, and add the staging branch to `pollForDownloadOrTakeOver`.
- [x] 6.3 Test: when staged compression differs from the requested compression, the reader transcodes at parity with the same-pod path (`cache.go:1307–1330`) and advertises the served compression.
- [x] 6.4 Test: a producer stall before completion surfaces as a stream error, never a truncated clean-EOF HTTP 200.

## 7. Read-path precedence during chunking window

- [x] 7.1 Test: with `total_chunks == 0` and staging parts present, the read path serves from staging and does NOT enter `streamProgressiveChunks`.
- [x] 7.2 Implement the precedence check (prefer staging over progressive chunks) in the chunk-serving entry point.
- [x] 7.3 Test: with `total_chunks == 0` and no staging present, it falls back to `streamProgressiveChunks`; with `total_chunks > 0`, steady-state chunk serving is unchanged.

## 8. GC / retention

- [x] 8.1 Test: after the final representation is committed + retention grace elapses, staging parts and the `staging_state` record are deleted and subsequent reads serve from the final representation.
- [x] 8.2 Implement event-driven reclaim on ingest completion (drain in-flight readers, then delete after grace).
- [x] 8.3 Test: an orphaned staging record (holder died, never taken over) is reclaimed by the periodic sweep keyed off `created_at` + `status`. [liveness uses updated_at staleness rather than created_at-as-death-proxy, per the #1230 lesson; created_at remains the fallback]
- [x] 8.4 Implement the periodic sweep.

## 9. Holder-death takeover (restart from zero)

- [x] 9.1 Test: when the staging holder dies and a waiter re-acquires the expired lock, the waiter restarts the download from zero, the dead holder's partial parts are discarded, and `staging_state` is reset.
- [x] 9.2 Implement takeover reset hooked into the existing `pollForDownloadOrTakeOver` re-acquisition path; re-stage from zero if contention persists. [takeover is now attempted before serving stale staging, so a dead holder's partial parts are never tailed as complete]
- [x] 9.3 Test: a reader is never served a truncated NAR across a holder-death + takeover transition.

## 10. Helm chart — HA validation + remove `iLoveTimeouts` (BREAKING)

- [x] 10.1 `values.yaml`: add `config.inflightStaging.enabled` (+ retention / part-size keys); **remove** `config.cdc.iLoveTimeouts`. [also wired `config.inflightStaging.*` into configmap.yaml]
- [x] 10.2 `_helpers.tpl`: HA guard passes iff `config.cdc.enabled` OR `config.inflightStaging.enabled`; remove the `iLoveTimeouts` clause; update the failure message to name both options + reference #660.
- [x] 10.3 `tests/validation_test.yaml`: drop the bypass case; add staging-passes, cdc-passes, and neither-fails cases. Run `helm unittest charts/ncps` to green.
- [x] 10.4 `nix/k8s-tests/config.nix`: switch the two `cdc.iLoveTimeouts = true` permutations (~225, ~252) and the `iLoveTimeouts` logic (~615) to `inflightStaging.enabled`.

## 11. Documentation (`docs/docs`)

- [x] 11.1 HA/deployment guidance: document that HA requires CDC **or** in-flight staging, with staging as the lightweight default; note request-affinity routing as an optional optimization.
- [x] 11.2 Document the three new flags (enable / retention / part-size) in the Configuration Reference.
- [x] 11.3 Update the Helm Chart Reference: add the `config.inflightStaging.*` rows; remove the `config.cdc.iLoveTimeouts` row.

## 12. CHANGELOG

- [x] 12.1 `Added`: in-flight NAR staging for cross-pod serving during download (closes #660 / #1289), with the three flags.
- [x] 12.2 `Changed`/`Removed`: **BREAKING** — `config.cdc.iLoveTimeouts` removed; migration is to set `config.inflightStaging.enabled=true` (or `config.cdc.enabled=true`).

## 13. End-to-end verification

- [x] 13.1 Integration test (Redis locker + S3): two replicas, large NAR, concurrent pull during download serves a complete NAR cross-pod; assert no truncation and the `cache_distributed_test.go:645` scenario passes. [added `InflightStagingCrossPodServe`; passes with Redis (`NCPS_ENABLE_REDIS_TESTS=1`), all 3 replicas get a byte-correct NAR; shared local dir stands in for S3 as in the sibling scenarios]
- [x] 13.2 Confirm zero-overhead: with the feature disabled, and with it enabled-but-uncontended, no staging parts/records are created. [covered deterministically by the group-5 unit tests `TestStageInflightNar_DisabledDoesNothing` and `TestStageInflightNar_NoRequestNoStaging`]
- [x] 13.3 Run `task fmt`, `task lint`, `task test`, `task ent:check`, and `helm unittest charts/ncps` — all exit zero.

## 14. OpenSpec archive (final/top branch of the stack)

> This is the ONLY branch that commits `openspec/`. Do it last, after groups 1–13 are all
> implemented and on their stacked branches.

- [x] 14.1 Run `/opsx:verify` for `serve-whole-nar-in-flight`; resolve any gaps between the
      implementation and the specs/design.
- [x] 14.2 Run `/opsx:archive` (syncs the delta specs into `openspec/specs/` and moves the
      change to `openspec/changes/archive/`).
- [x] 14.3 `/gs-create` the archive branch: stage `openspec/` (now including the synced specs +
      archived change + this completed `tasks.md`) and commit as the top branch of the stack.
