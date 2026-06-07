## Why

`migrate-chunks-to-nar` (the self-completing-cdc-drain change) drives the chunked count to zero *when an operator runs a drain*. But fsck is the tool installations actually run regularly (often daily), and it should also keep CDC residue from accumulating without requiring a deliberate drain. fsck must do this **without ever harming a legitimately chunked NAR** during active CDC — so it cannot simply purge anything it can't de-chunk on sight.

## What Changes

A new fsck check + `--repair` action for chunked `nar_file` rows, with two tiers:

- **Recoverable → normalize immediately (safe).** A chunked NAR whose narinfo references it with a valid NarHash but advertises a non-none URL is inconsistent but fixable: relink and normalize the narinfo URL to Compression:none. This never touches chunks and is safe in any CDC state.
- **Un-de-chunkable → mark, then purge after a grace window (deferred).** A chunked NAR with no narinfo carrying a resolvable NarHash is flagged with a persistent timestamp on first detection. It is purged only on a **later** fsck run, once the flag has aged past a grace window (default ~24h) **and** it is still un-de-chunkable. If it becomes recoverable or de-chunked in the meantime, the flag is cleared and it is never purged. The grace window — two runs a day apart — prevents purging transient states (a NAR mid-chunking, a narinfo not yet written).

This requires a new nullable `nar_file.dechunk_residue_flagged_at` column (forward-only migration).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `fsck`: fsck MUST detect chunked-NAR residue and heal it — immediately normalizing recoverable inconsistencies, and marking-then-purging un-de-chunkable rows only after a grace window, so legitimately chunked NARs are never harmed.
- `data-model`: `nar_file` gains a nullable `dechunk_residue_flagged_at` timestamp recording when fsck first flagged the row as un-de-chunkable residue.

## Impact

- `ent/schema/nar_file.go` + `migrations/{sqlite,postgres,mysql}/*`: new nullable column.
- `pkg/ncps/fsck.go`: the residue detection + repair tiers, with a configurable grace window.
- Validated against the real production 116 stragglers (run after the de-chunk pass) and by the CDC-lifecycle e2e tests.
