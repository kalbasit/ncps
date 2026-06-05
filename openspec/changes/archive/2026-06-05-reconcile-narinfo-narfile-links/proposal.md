## Why

A rare race leaves CDC-chunked `nar_file` rows with no `narinfo_nar_files` join link (the link is created in the narinfo-write path, decoupled from the async chunking that finalizes the `nar_file`). ~0.8% of CDC NARs in a production snapshot were affected. The consequences are severe: the `migrate-chunks-to-nar` de-chunk pass resolves its verification NarHash *only* through that link, so it skips every unlinked NAR forever — the cache can never finish draining and exit drain mode — and `fsck` classifies those valid, reachable narinfos as orphans and **deletes** them, destroying live metadata and orphaning their NARs.

## What Changes

- **De-chunk robustness**: `linkedNarinfoNarHash` falls back to resolving the narinfo by the NAR's `Compression:none` URL when no join link exists, so an unlinked chunked NAR is still content-verified and drained instead of skipped.
- **fsck safety**: the repair phase recreates a missing `narinfo_nar_files` link (from the nar_file the narinfo's URL references) instead of deleting the narinfo; only a narinfo with no backing nar_file anywhere is still deleted. This removes a data-loss hazard.
- **Creation-race prevention**: `checkAndFixNarInfosForNar` (invoked right after CDC chunking completes) reconciles the link via an idempotent upsert, closing the race at the source.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `chunks-to-nar-migration`: the reverse (de-chunk) migration MUST resolve the verification NarHash via the narinfo URL when no join link exists, rather than skipping the NAR.
- `fsck`: an unlinked narinfo whose backing nar_file exists MUST be repaired (link recreated), not deleted; only genuinely backing-less narinfos are reclaimed.
- `cdc-chunking`: completing a CDC chunking operation MUST reconcile the `narinfo_nar_files` link for every narinfo whose URL references the finalized nar_file.
- `cdc-drain-mode`: drain completion (the set of chunked NARs reaching zero) MUST be reachable even for NARs whose join link was never created.

## Impact

- `pkg/cache/cache.go`: `linkedNarinfoNarHash` (de-chunk hash resolution), `checkAndFixNarInfosForNar` (link reconciliation on chunk completion).
- `pkg/ncps/fsck.go`: `repairFsckIssues` + new `relinkNarInfoToBackingNarFile` helper.
- Operational: a one-time prod remediation re-runs `migrate-chunks-to-nar` against the deployed image to drain the previously-stranded chunked NARs, then drain mode is exited.
- No schema change; no migration.
