## Why

The `chunks-to-nar-migration` spec reads as self-contradictory: one requirement says an un-verifiable chunked NAR (no linked/hash-matched narinfo NarHash) SHALL be **skipped** and SHALL NOT be deleted or truncated, while another says the de-chunk pass SHALL **purge** un-verifiable NARs and MUST drive the chunked count to zero. A reviewer (CodeRabbit, PR #1342) flagged this as ambiguous.

Tracing the code shows there is **no behavioral conflict** — the two requirements describe two different layers, and the spec just fails to say so:

- `Cache.MigrateChunksToNar` (the single-NAR operation) returns `ErrNoNarHashToVerify` and **leaves the NAR chunked**, never deleting what it cannot content-verify (`pkg/cache/cache.go:8371-8377`). That is the "skip" behavior — verified-or-nothing.
- The batch pass (`pkg/ncps/migrate_chunks_to_nar.go:433-458`) catches that error and calls `PurgeChunkedNar`, so the **pass** drives the chunked count to zero. That is the "purge" behavior.

## What Changes

- Clarify in the spec that **skip** is the single-`MigrateChunksToNar`-operation behavior (it refuses to de-chunk or delete a NAR it cannot verify; it returns `ErrNoNarHashToVerify` and leaves it chunked), and **purge / drive-to-zero** is the **batch-pass** behavior (the driver purges what the operation skipped so a later request re-fetches from upstream).
- Reword the "NAR with neither a link nor a hash-matched narinfo is skipped" scenario to scope it to the single operation and note the pass subsequently purges it — removing the apparent contradiction.
- No code change: the implementation already matches this layered policy.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `chunks-to-nar-migration`: clarify the layering between the single-operation "skip (verified-or-nothing)" behavior and the pass-level "purge / drive chunked count to zero" behavior so the requirements are mutually consistent.

## Non-goals

- No change to de-chunk behavior, purge behavior, or verified-or-nothing reconstruction — this is a documentation-consistency change only.
- No change to the hash-aware narinfo matching introduced by `fix-dechunk-unlinked-narinfo-url-match` (that change stands; this only reconciles the skip-vs-purge wording).

## Impact

- **Code**: none. If verification during apply reveals the code does NOT match the documented layered policy, that divergence will be raised rather than silently "fixed" here.
- **Spec**: `openspec/specs/chunks-to-nar-migration/spec.md` (two requirements reworded for layering clarity).
- **I/O / network / memory**: none.
