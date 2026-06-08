## Context

`upstream.GetNarInfo` (`pkg/cache/upstream/cache.go:468-477`) hard-rejects any narinfo where
`FileSize == 0 && Compression != none` with
`invalid narinfo: FileSize is missing for a compressed NAR`. Several real upstreams (niks3,
nix-serve-style servers) omit the *optional* `FileSize`/`FileHash` fields on compressed
NARs, so every request 404s and the upstream is unusable (issue #1314). The existing TODO at
that branch already anticipated the fix: compute the values from the NAR "when it arrives",
deferred because that path is asynchronous.

Relevant current behavior:
- The narinfo is persisted **synchronously** in the download goroutine via
  `storeInDatabase` (`pkg/cache/cache.go:4104`).
- The NAR is fetched **asynchronously** by `prePullNar` (`cache.go:4050`), launched *before*
  the narinfo is stored. `prePullNar` → `pullNarIntoStore` (`cache.go:2996`) streams the
  compressed bytes into `narStore.PutNar`.
- The compressed on-disk size is already known at store time (`storedFileSize`,
  `cache.go:3582`) and tracked on the `nar_file` row. The compressed **hash** is not
  computed anywhere today.
- narinfo `file_hash` (string, nullable) and `file_size` (int64, nullable) columns already
  exist (`ent/schema/narinfo.go:61-62`); they are written by
  `applyNarInfoCreate`/`applyNarInfoUpdate` (`cache.go:5371`, `cache.go:5416`) only when
  non-nil/non-zero, else left NULL.
- `Compression: none` and CDC paths deliberately null `FileHash`/`FileSize`
  (`cache.go:4075-4077`, `4179-4181`) — these are out of scope and must stay unchanged.

## Goals / Non-Goals

**Goals:**
- Stop rejecting compressed upstream narinfos that omit `FileSize`/`FileHash`.
- For compressed NARs served under their original compression, always serve a correct
  `FileSize` and `FileHash`, computing them from the stored compressed bytes when upstream
  omits them, and backfilling them into the persisted narinfo.
- Preserve upstream-provided `FileSize`/`FileHash` verbatim (no recompute).

**Non-Goals:**
- No change to `Compression: none` or CDC handling (those intentionally null these fields).
- No re-hashing of NARs already stored before this change.
- No DB schema/migration change (columns already exist and are nullable).
- No change to NarHash/NarSize verification or the NAR bytes themselves.

## Decisions

### 1. Remove the rejection; do not synthesize a value at narinfo-parse time
In `upstream.GetNarInfo`, delete the `Compression != none` rejection branch. Keep the
existing `FileSize = NarSize` fallback only for the uncompressed (`none`) case. For a
compressed NAR with no `FileSize`, leave `FileSize == 0` / `FileHash == nil` and let the
pull path fill them. Rationale: the upstream layer only sees the narinfo, not the NAR bytes,
so it cannot compute the *compressed* file's size/hash correctly (NarSize ≠ compressed
size). Synthesizing `FileSize = NarSize` for a compressed NAR (the old uncompressed
fallback) would advertise a wrong size — worse than omitting it.

### 2. Reuse the existing post-store fixup; only FileHash is genuinely new
Implementation revealed that the existing post-store fixup already backfills `FileSize`:
`pullNarIntoStore` (and `PutNar`) call `checkAndFixNarInfosForNar` → `CheckAndFixNarInfo`,
which reads the stored compressed size from `nar_file.file_size` (`getNarActualSize`) and
corrects the narinfo via `fixNarInfoFileSize`. That path simply never ran for compressed
NARs because the upstream layer rejected them first (Decision 1 removes that block). So the
only missing computation is `FileHash`. The fix extends `CheckAndFixNarInfo`'s compressed
branch with `computeStoredNarFileHash`, which streams the stored compressed NAR
(`narStore.GetNar`) through a single `sha256` pass (constant memory, no full-file buffering)
and formats it as `nixhash.MustNewHashWithEncoding(SHA256, sum, NixBase32, true)` →
`sha256:<nixbase32>` (the constructor used in `migrate_chunks_to_nar`).

*Alternative considered:* tap the in-flight pull stream with `io.TeeReader` and persist the
hash on a new `nar_file` column. Rejected — it requires a schema migration (violating a
non-goal), touches the hot streaming path for every NAR, and duplicates the existing
read-after-store fixup architecture that already handles `FileSize`. The chosen approach
incurs one extra streaming read, but only for compressed NARs whose upstream omitted
`FileHash` (the niks3 minority), once per NAR.

### 3. Backfill is the existing fixup's eventual-completeness model
The narinfo row is written (`storeInDatabase`) before the async NAR download finishes, so the
computed values cannot be present at first store. `checkAndFixNarInfosForNar` runs after the
NAR lands and reconciles the narinfo by URL — the same mechanism that already corrected
`FileSize`. The first narinfo response in the (brief) window before the NAR lands may omit
these fields — acceptable because they are optional in the narinfo format and Nix verifies
the closure against `NarHash` after decompression regardless. The backfill UPDATE is
idempotent and conditional (set `file_hash` only when currently NULL/empty).

*Alternative considered:* block `storeInDatabase` until the NAR finishes downloading so the
first serve is complete. Rejected — this is exactly what the original TODO flagged ("since
this is async it breaks a lot of tests"); it adds the full NAR-download latency to the
narinfo response and serializes two operations that are intentionally decoupled.

### 4. Only compute when needed
Skip the SHA-256 pass when the narinfo already carries `FileHash` (upstream supplied it),
when compression is `none` (handled by the earlier `checkAndFixNarInfoNoCompression`
branch), or when the NAR is not whole-file in the store (`hasNarInStore` is false — CDC
narinfos are normalized to `none`). Rationale: avoids hashing on the common path.

## Risks / Trade-offs

- **Transient narinfo without FileHash/FileSize** (between first serve and NAR landing) →
  Mitigation: optional fields; Nix tolerates their absence and verifies via NarHash. Backfill
  closes the gap on the next request.
- **Extra CPU for a SHA-256 pass** on compressed NARs lacking an upstream FileHash →
  Mitigation: only hash when needed (Decision 4); single streaming pass, no extra I/O.
- **Concurrent narinfo writes** could race the backfill UPDATE → Mitigation: conditional
  update (set only when NULL) + the existing per-hash narinfo write lock; the update is
  idempotent.
- **Re-pull after eviction** recomputes the hash → Mitigation: idempotent; same bytes yield
  the same `FileHash`/`FileSize`.
- **Accidentally writing these fields on the none/CDC paths** → Mitigation: gate strictly on
  `Compression != none` and non-CDC storage; covered by tests asserting those paths still
  null the fields.

## Migration Plan

- No DB migration: `file_hash`/`file_size` columns already exist and are nullable.
- Pure forward, backward-compatible change. Rollback = revert the code; previously
  backfilled rows simply carry valid optional fields that an older binary ignores/overwrites.
- No config flag; the behavior is strictly more permissive (accepts narinfos previously
  rejected) and otherwise transparent.

## Open Questions

- None blocking. (The opaque-URL compressed case at `cache.go:4078-4085` follows the same
  compressed-serve path and is covered by the same compute/backfill logic.)
