# chunks-to-nar-migration Specification

## Purpose

Defines the `migrate-chunks-to-nar` operation — the reverse of `migrate-nar-to-chunks` — which reconstructs CDC-chunked NARs back into verified whole files so a deployment can safely exit CDC, with idempotent/resumable execution and dedup-safe chunk reclamation.
## Requirements
### Requirement: Reconstruct a whole NAR from its chunks, verified against the recorded hash

The system SHALL provide a `migrate-chunks-to-nar` operation that, for a chunked `nar_file`, reconstructs the whole NAR by concatenating its chunks in `chunk_index` order, verifies the reconstructed bytes against the recorded `NarHash` (and `NarSize`), writes the whole NAR to the NAR store, and flips the `nar_file` record to the whole-file representation. Verification is mandatory: if the reconstructed hash or size does not match the recorded values, or if a referenced chunk is absent from the chunk store, the operation SHALL purge the corrupt entry — delete all `nar_file_chunks` links for the affected `nar_file`, delete any chunk objects that become unreferenced as a result, and delete the `nar_file` record — so the NAR can be re-fetched from upstream on the next `GetNar` request. The purge SHALL NOT write any whole-file bytes to the NAR store for the affected hash.

#### Scenario: Chunked NAR is reconstructed, verified, and stored whole

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and all chunks present
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL stream the chunks in order, compute the NAR hash, and confirm it equals the recorded `NarHash`
- **AND** SHALL write the whole NAR to the NAR store
- **AND** the resulting `nar_file` record SHALL represent a whole-file NAR (`total_chunks = 0`, no chunk links)
- **AND** a subsequent `GetNar` for `H` SHALL serve the whole file

#### Scenario: Hash mismatch purges the NAR and evicts it for re-fetch

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstructed bytes do not match `NarHash`
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL delete all `nar_file_chunks` links for `H`
- **AND** SHALL delete any chunk objects that are now unreferenced (not linked to any other `nar_file`)
- **AND** SHALL delete the `nar_file` record for `H`
- **AND** SHALL NOT write whole-file bytes to the NAR store for `H`
- **AND** a subsequent `GetNar` for `H` SHALL trigger a fresh upstream fetch

#### Scenario: Missing chunk purges the NAR and evicts it for re-fetch

- **GIVEN** a chunked `nar_file` for hash `H` with at least one referenced chunk absent from the chunk store
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL delete all `nar_file_chunks` links for `H`
- **AND** SHALL delete any chunk objects that are now unreferenced
- **AND** SHALL delete the `nar_file` record for `H`
- **AND** a subsequent `GetNar` for `H` SHALL trigger a fresh upstream fetch

#### Scenario: Purge retains narinfo record

- **GIVEN** a chunked `nar_file` for hash `H` with a linked `narinfo` record
- **WHEN** `migrate-chunks-to-nar` purges `H` due to hash mismatch or missing chunk
- **THEN** the `narinfo` record for `H` SHALL remain in the database
- **AND** a subsequent `GetNarInfo` for `H` SHALL return the narinfo
- **AND** the subsequent `GetNar` for `H` SHALL fetch from upstream and re-store the NAR correctly

#### Scenario: Purge is dedup-safe for shared chunks

- **GIVEN** chunk `C` is referenced by both hash `H1` (being purged) and hash `H2` (still chunked)
- **WHEN** `migrate-chunks-to-nar` purges `H1`
- **THEN** chunk `C` SHALL remain in the chunk store because `H2` still references it

### Requirement: Migration MUST be idempotent and resumable

The operation SHALL be safe to re-run and safe to resume after interruption. A NAR already in the whole-file representation SHALL be skipped. An interruption SHALL NOT leave a half-written whole file presented as complete, nor delete chunks before the whole file is durably stored and the record flipped.

#### Scenario: Already-whole NAR is skipped

- **GIVEN** a `nar_file` for hash `H` that is already whole-file (`total_chunks = 0`)
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL treat `H` as already migrated and make no changes

#### Scenario: Re-run after interruption completes cleanly

- **GIVEN** a run was interrupted while migrating hash `H` (e.g. after writing the whole file but before flipping the record, or before reclaiming chunks)
- **WHEN** `migrate-chunks-to-nar` is run again
- **THEN** it SHALL bring `H` to a consistent whole-file state without producing a corrupt or short whole file
- **AND** SHALL NOT require manual cleanup of partial artifacts

### Requirement: Chunk reclamation MUST be deferred by default and always dedup-safe

The system SHALL NOT delete a NAR's chunks as part of de-chunking by default: a concurrent serve that began streaming from chunks before the record was flipped may still be reading those chunk files, and deleting them mid-stream would truncate that transfer. By default the now-unreferenced chunks SHALL be left for the regular garbage collector to reclaim. The operation SHALL provide an explicit opt-in (`--force-reclaim`) for callers that assert traffic is drained (e.g. a maintenance-window run), which reclaims unreferenced chunks immediately. In either path a chunk SHALL be deleted only when no `nar_file` references it (no remaining `nar_file_chunks` links); a chunk shared with another still-chunked NAR SHALL NEVER be deleted.

#### Scenario: Default run does not delete chunks

- **GIVEN** a chunked NAR `H` whose chunks are referenced only by `H`
- **WHEN** `H` is migrated to whole-file without `--force-reclaim`
- **THEN** the `nar_file` SHALL be flipped to whole-file (links removed, `total_chunks = 0`)
- **AND** the chunk objects SHALL remain in the store (left for the GC), so an in-flight chunk-serve is not truncated

#### Scenario: Force-reclaim deletes a now-orphaned chunk

- **GIVEN** chunk `C` is referenced only by hash `H` (being migrated)
- **WHEN** `H` is migrated to whole-file with `--force-reclaim`
- **THEN** chunk `C` SHALL be deleted from the chunk store as it is now unreferenced

#### Scenario: Shared chunk is retained even with force-reclaim

- **GIVEN** chunk `C` is referenced by both hash `H1` (migrated with `--force-reclaim`) and hash `H2` (still chunked)
- **WHEN** `H1` is migrated to whole-file and its chunk links removed
- **THEN** chunk `C` SHALL remain in the chunk store because `H2` still references it

### Requirement: A dry-run mode MUST make no changes

The operation SHALL support a `--dry-run` flag that reports what would be migrated and reclaimed without writing whole files, mutating records, or deleting chunks.

#### Scenario: Dry-run reports without mutating

- **GIVEN** chunked NARs eligible for migration
- **WHEN** `migrate-chunks-to-nar --dry-run` is run
- **THEN** the system SHALL report the NARs it would migrate and chunks it would reclaim
- **AND** SHALL NOT write to the NAR store, mutate `nar_file` records, or delete chunks

### Requirement: A per-NAR failure MUST NOT abort the batch

When processing many NARs, a failure on one NAR (hash mismatch, missing chunk, I/O error) SHALL be recorded and SHALL NOT prevent the remaining NARs from being processed. Hash mismatch and missing-chunk failures are unrecoverable and result in a purge; transient errors (I/O errors, lock failures, query errors) are counted as failures. The command SHALL exit non-zero only when at least one transient failure occurred. The command SHALL exit 0 when all NARs were either successfully migrated or purged. The command SHALL report migrated, purged, skipped, and failed counts in the final summary log line.

#### Scenario: Hash-mismatch NAR is purged; batch continues; exit 0

- **GIVEN** a batch where hash `H_bad` fails verification and other NARs are valid
- **WHEN** `migrate-chunks-to-nar` runs over the batch
- **THEN** every valid NAR SHALL be migrated
- **AND** `H_bad` SHALL be purged (nar_file + orphaned chunks deleted)
- **AND** the command SHALL exit 0
- **AND** the summary log SHALL report `purged=1`

#### Scenario: Transient I/O error still causes non-zero exit

- **GIVEN** a batch where hash `H_io` fails due to a transient I/O error (not a hash mismatch)
- **WHEN** `migrate-chunks-to-nar` runs over the batch
- **THEN** `H_io` SHALL be counted as failed (not purged)
- **AND** the command SHALL exit non-zero
- **AND** the summary log SHALL report `failed=1`

### Requirement: The migrate-chunks-to-nar Helm Job MUST be a regular release resource, not a Helm hook

The Job rendered by `migrateChunksToNar.enabled: true` SHALL carry no `helm.sh/hook*` or `argocd.argoproj.io/hook*` annotations. It is a regular Kubernetes Job included in the Helm release manifest. ArgoCD syncs it alongside other resources and does not treat its outcome as a gate on sync success.

#### Scenario: Job is rendered without hook annotations

- **WHEN** the Helm chart is rendered with `migrateChunksToNar.enabled: true`
- **THEN** the resulting Job manifest SHALL NOT contain `helm.sh/hook`, `helm.sh/hook-weight`, or `helm.sh/hook-delete-policy` annotations

#### Scenario: Job is auto-deleted after completion

- **WHEN** the Job finishes (success or failure)
- **THEN** Kubernetes SHALL garbage-collect the Job after `migrateChunksToNar.job.ttlSecondsAfterFinished` seconds (default 3600)

### Requirement: De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists

When de-chunking a NAR, the system resolves the expected NarHash from the linked narinfo in order to content-verify the reconstruction (verified-or-nothing). When the `narinfo_nar_files` join link is absent — a known race leaves CDC-chunked `nar_file` rows unlinked — the system SHALL fall back to resolving the narinfo by the NAR's hash, matching **hash-aware** rather than by raw URL prefix: a candidate narinfo references the NAR when its URL, parsed and normalized (narinfo-hash prefix stripped), has the same NAR hash. This SHALL cover both canonical URLs (`nar/<hash>.nar[.<ext>]`) and nix-serve-style prefixed URLs (`nar/<narinfoHash>-<hash>.nar[.<ext>]`). Only when no narinfo references the NAR by either the join link or a hash-matched URL SHALL the de-chunk be skipped for want of a verification hash.

#### Scenario: Unlinked chunked NAR is de-chunked via the URL-resolved NarHash

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and intact chunk links
- **AND** a narinfo with URL `nar/<H>.nar` carrying a recorded NarHash
- **AND** NO `narinfo_nar_files` link between that narinfo and the `nar_file`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL resolve the verification NarHash from the narinfo found by URL
- **AND** SHALL reconstruct the whole NAR from its chunks
- **AND** SHALL content-verify the reconstruction against that NarHash
- **AND** SHALL flip the record to whole-file (`total_chunks = 0`)

#### Scenario: Unlinked narinfo with a nix-serve-style prefixed URL is matched hash-aware

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0`
- **AND** a narinfo with a prefixed URL `nar/<narinfoHash>-<H>.nar.xz` carrying a recorded NarHash
- **AND** NO `narinfo_nar_files` link between that narinfo and the `nar_file`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL recognize the narinfo as referencing `H` by its normalized embedded hash (not by a raw `nar/<H>.` prefix)
- **AND** SHALL resolve the verification NarHash from it and de-chunk `H`

#### Scenario: NAR with neither a link nor a hash-matched narinfo is skipped

- **GIVEN** a chunked `nar_file` for hash `H` with no `narinfo_nar_files` link
- **AND** no narinfo whose URL normalizes to hash `H`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL skip de-chunking (no NarHash to verify against)
- **AND** SHALL NOT delete or truncate the NAR

### Requirement: De-chunk MUST content-verify the reconstructed NAR before committing

The de-chunk pass SHALL commit a NAR to whole-file storage only after it reconstructs the NAR from its chunks, computes the SHA-256 of the reconstructed bytes, and confirms that digest equals the resolved NarHash. The verification hash is the uncompressed NAR content hash, which is identical across every narinfo referencing the NAR regardless of the compression that narinfo's URL advertises; resolving it from any referencing narinfo (by NAR hash) therefore does NOT weaken verification. On a digest mismatch — or any reconstruction failure — the pass SHALL NOT write the whole file and SHALL NOT flip the record; it SHALL purge the chunked `nar_file` instead. The pass SHALL NEVER persist a de-chunked NAR it did not content-verify.

#### Scenario: Reconstructed-hash mismatch purges, never commits

- **GIVEN** a chunked `nar_file` for hash `H` and a resolved NarHash
- **WHEN** the reconstructed NAR's SHA-256 does NOT equal the resolved NarHash
- **THEN** the pass SHALL NOT write a whole file for `H`
- **AND** SHALL NOT flip `H` to `total_chunks = 0`
- **AND** SHALL purge the chunked `nar_file` for `H`

#### Scenario: Reconstructed-hash match commits

- **GIVEN** a chunked `nar_file` for hash `H` and a resolved NarHash
- **WHEN** the reconstructed NAR's SHA-256 equals the resolved NarHash
- **THEN** the pass SHALL write the whole file and flip `H` to `total_chunks = 0`

### Requirement: The de-chunk pass MUST always drive the chunked count to zero

A full `migrate-chunks-to-nar` pass over all chunked `nar_file` rows SHALL leave no row with `total_chunks > 0`. For every chunked NAR the pass SHALL either de-chunk it to whole-file storage or purge it; it SHALL NOT leave a NAR chunked because it could not resolve a verification hash or could not reconstruct the NAR.

#### Scenario: NarHash is resolved by NAR hash from any referencing narinfo

- **GIVEN** a chunked `nar_file` for hash `H` with no join link
- **AND** the only narinfo carrying a `nar_hash` for `H` advertises a different-compression URL (e.g. `nar/<H>.nar.xz`), not the bare `nar/<H>.nar`
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL resolve the verification NarHash from that narinfo (matched by NAR hash, not by exact URL)
- **AND** SHALL de-chunk `H` to whole-file storage

#### Scenario: Un-verifiable NAR is purged, not skipped

- **GIVEN** a chunked `nar_file` for hash `H` with no narinfo carrying a resolvable `nar_hash`
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL purge the chunked `nar_file` (removing its chunk links so a later request re-pulls from upstream)
- **AND** SHALL NOT leave `H` chunked
- **AND** SHALL NOT count `H` as a hard failure that aborts the run

#### Scenario: Hard reconstruction failure is purged, not failed-and-left

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstruction fails (corrupt or missing chunks)
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL purge the chunked `nar_file`
- **AND** SHALL NOT leave `H` chunked

### Requirement: De-chunking MUST normalize the narinfo URL to none

When the de-chunk pass converts a NAR to whole-file (`Compression:none`) storage, it SHALL update every narinfo referencing that NAR to advertise the Compression:none URL (`nar/<H>.nar`, FileHash null, FileSize null), so the persisted narinfo is consistent with the whole-file storage and does not depend on serve-time chunk-based normalization. "Every narinfo referencing that NAR" SHALL be identified by the `narinfo_nar_files` join link OR, for unlinked rows, by a **hash-aware** URL match (the candidate URL parsed and normalized to the same NAR hash **and query**) — covering both canonical and nix-serve-style prefixed URLs — not by a raw URL prefix that would miss prefixed URLs. Because `nar_file` rows are keyed by `(hash, compression, query)`, a narinfo whose URL normalizes to the same hash but a **different query** is a distinct variant and SHALL NOT be rewritten by this normalization.

#### Scenario: A de-chunked NAR's narinfo advertises none

- **GIVEN** a chunked NAR whose narinfo advertises `nar/<H>.nar.xz`
- **WHEN** the de-chunk pass de-chunks it to `none/whole`
- **THEN** the narinfo SHALL be updated to URL `nar/<H>.nar` and Compression none
- **AND** a subsequent serve of that narinfo SHALL NOT 404 the NAR

#### Scenario: An unlinked, prefixed-URL narinfo is normalized on de-chunk

- **GIVEN** a de-chunked NAR for hash `H`
- **AND** a narinfo with a prefixed URL `nar/<narinfoHash>-<H>.nar.xz` and NO join link to the `nar_file`
- **WHEN** the de-chunk pass normalizes referencing narinfos
- **THEN** the prefixed-URL narinfo SHALL be updated to URL `nar/<H>.nar` and Compression none
- **AND** a subsequent serve of that narinfo SHALL NOT 404 the NAR

#### Scenario: A same-hash, different-query narinfo is not rewritten

- **GIVEN** a de-chunked NAR for hash `H` with query `""`
- **AND** a narinfo for the same hash but a different query (URL `nar/<H>.nar.xz?foo=bar`), referencing a distinct `nar_file` variant
- **WHEN** the de-chunk pass normalizes referencing narinfos
- **THEN** the different-query narinfo's URL SHALL be left unchanged (its query SHALL NOT be clobbered)

