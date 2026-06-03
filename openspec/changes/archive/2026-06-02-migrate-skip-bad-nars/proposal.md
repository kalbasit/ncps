## Why

After the CDC→whole-file migration completed its full 45k-NAR run, 168 NARs consistently fail reconstruction with hash mismatches every time and will never migrate successfully — their chunked data is permanently corrupt. Because the command exits non-zero whenever any NAR fails, the Kubernetes Job is stuck in a `Failed` state even though 99.6% of the work is done. These 168 entries need to be purged so they can be re-fetched clean from upstream on next access.

## What Changes

- When `migrate-chunks-to-nar` detects an unrecoverable failure (hash mismatch or size mismatch) for a NAR, it SHALL purge that entry — deleting the `nar_file` record and all associated chunk files — rather than recording an error and leaving the broken chunks in place.
- After purging, the NAR is treated as evicted: the next `GetNar` request for it will trigger a normal upstream fetch and re-store the entry correctly.
- The command exits 0 after processing all NARs; purged entries are reported in a summary log line but are not counted as failures.

## Non-goals

- Root-causing why 168 NARs have corrupt chunk data.
- Preventing new corrupt chunk entries from being created.
- Changing the behavior for missing-chunk failures (same purge logic applies; both are unrecoverable).
- Any changes to the normal (non-migration) NAR serve path.

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `chunks-to-nar-migration`: The "hash mismatch" and "missing chunk" scenarios change from abort-without-mutation to purge-and-evict. The command's exit-code contract also changes: exits 0 after a complete run (purged entries do not constitute failures).

## Impact

- **Code**: `migrate-chunks-to-nar` command implementation; storage delete path for `nar_file` + chunks.
- **Database**: On mismatch, deletes the `nar_file` row and its `nar_file_chunks` links for the affected hash.
- **Storage**: On mismatch, deletes the associated chunk objects from the chunk store.
- **I/O**: Negligible — at most 168 additional small deletes in a one-time migration run.
- **Network / memory**: No impact.
- **Kubernetes Job**: Will exit 0 after a complete run, allowing ArgoCD to treat it as succeeded.
