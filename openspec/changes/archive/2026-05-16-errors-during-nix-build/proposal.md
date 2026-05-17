## Why

Nix clients report `error: input compression not recognized` and `error: bad archive: input doesn't look like a Nix archive` when downloading from ncps in CDC mode. The root cause is likely corrupt or truncated chunk data stored before recent fixes (#1171, #1172), but `fsck` cannot detect this because it only checks chunk count and presence — it never verifies that chunk content actually hashes to its recorded hash, nor that the assembled NAR matches the `NarHash` from the narinfo. Without content-level integrity checks, fsck gives a false "all clear" while Nix clients fail at download time.

## What Changes

- **New fsck CDC check**: For each chunk in storage, decompress and verify its SHA-256 digest matches the stored hash in the DB. Flag mismatches as a new `narFilesWithCorruptChunks` category.
- **New fsck CDC check**: For NARs where all chunks pass the hash check, assemble and hash the complete NAR stream and compare against the linked narinfo's `NarHash`. Flag mismatches as a new `narFilesWithHashMismatch` category.
- Both new checks are gated behind a `--verify-content` flag (default: off) because they require full I/O read of chunk data and are expensive at scale.
- Repair path for both new categories: delete the corrupt `nar_file` record + orphaned chunks + orphaned narinfo (same cascade already used by `narFilesWithChunkIssues` repair).

## Capabilities

### New Capabilities

- `fsck --verify-content`: opt-in content-integrity verification that reads and hashes every chunk and the assembled NAR stream.
- `narFilesWithCorruptChunks`: new fsck result category — NARs with chunks whose decompressed content does not hash to their stored key.
- `narFilesWithHashMismatch`: new fsck result category — NARs whose assembled byte stream does not match the narinfo `NarHash`.

### Modified Capabilities

- `fsck` summary table gains two new CDC rows (`NAR files w/ corrupt chunks` and `NAR files w/ hash mismatch`) when `--verify-content` is set.
- `fsck` description updated to document the new checks and the `--verify-content` flag.

## Impact

- `pkg/ncps/fsck.go` — adds `narFilesWithCorruptChunks` and `narFilesWithHashMismatch` to `fsckResults`; new `collectNarFilesWithCorruptChunks`, `collectNarFilesWithHashMismatch` helpers; repair path extension; summary table rows; `--verify-content` flag.
- `pkg/storage/chunk/store.go` — no interface changes needed; callers use existing `GetChunk` and hash the output.
- `pkg/ncps/fsck_test.go` — new test cases covering corrupt chunk detection and hash mismatch detection.
- `charts/ncps/values.yaml` — new `fsck.verifyContent` boolean (default: `false`).
- `charts/ncps/templates/fsck-cronjob.yaml` — pass `--verify-content` when `fsck.verifyContent` is true.
- `docs/docs/User Guide/Operations/Integrity Check (fsck).md` — document `--verify-content`, its cost, and guidance on when to use it.
- `docs/docs/User Guide/Installation/Helm Chart.md` — document `fsck.verifyContent` chart value.
- Affected specs: `fsck`, `cdc-chunking`.
- No API surface changes; no database schema changes; no migration required.
- I/O impact: `--verify-content` reads every chunk byte in full — can be high on large caches; document this clearly in flag usage and docs.
