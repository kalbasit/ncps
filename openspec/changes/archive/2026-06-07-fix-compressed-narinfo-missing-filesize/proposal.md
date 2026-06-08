## Why

Some upstream binary caches (e.g. niks3, nix-serve-style servers) emit narinfos that
declare `Compression: zstd` but omit the optional `FileSize`/`FileHash` fields. ncps
currently treats a missing `FileSize` on a compressed NAR as fatal, returning
`invalid narinfo: FileSize is missing for a compressed NAR` and a 404 for **every**
request. This makes such upstreams entirely unusable through ncps (GitHub issue #1314).
`FileSize`/`FileHash` are optional in the narinfo format, so rejecting them is incorrect.

## What Changes

- Stop rejecting upstream narinfos that carry a non-`none` `Compression` but no
  `FileSize`/`FileHash`. Such narinfos SHALL be fetched and served instead of erroring.
- For compressed NARs, ncps SHALL always deliver a correct `FileSize` and `FileHash`
  downstream. When upstream omits them, ncps SHALL compute them itself from the stored
  compressed NAR bytes (a single streaming SHA-256 pass + byte length), then backfill the
  computed values into the persisted narinfo record.
- Because the NAR is fetched lazily (after the narinfo), the computation happens in the
  post-store fixup once the NAR has been stored; subsequent narinfo serves carry the
  backfilled values.
- Conventional narinfos that DO supply `FileSize`/`FileHash` continue to behave exactly as
  today (no recompute).
- Removes the stale `FileSize == 0 && Compression != none` rejection branch and its TODO.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `upstream-fetch-resilience`: add a requirement that an upstream narinfo declaring a
  compression algorithm but omitting the optional `FileSize`/`FileHash` fields MUST be
  tolerated and served, rather than rejected as invalid.

## Impact

- Code: `pkg/cache/upstream/cache.go` (`GetNarInfo`, the `FileSize == 0` branch around
  line 468) plus the post-store fixup (`CheckAndFixNarInfo`) where the stored compressed
  bytes are read to compute `FileSize`/`FileHash` and backfill them into the narinfo record.
- APIs: narinfo responses for affected store paths change from `404` to `200`, and carry a
  correct ncps-computed `FileSize`/`FileHash`.
- Dependencies / systems: none. No schema, migration, or storage-format change.

### Non-goals

- Changing behavior for narinfos that already provide a valid `FileSize`/`FileHash`
  (no recompute, no second-guessing upstream).
- Re-hashing already-stored NARs that predate this change (only NARs stored after the
  change get computed values).
- Any change to NAR storage layout, CDC chunking, or downstream caching semantics.

### Performance impact

- I/O: one extra streaming read of the stored compressed NAR to hash it, incurred only for
  affected NARs (compressed, missing an upstream `FileHash`); no full-file buffering.
- Network latency: none added; affected requests stop failing. Computed `FileSize`/
  `FileHash` are available once the NAR has been stored and the fixup has run.
- Memory: negligible — a single streaming hasher (constant-size state), no full-file
  buffering.
- CPU: one sha256 pass over the compressed bytes for affected NARs only (those lacking
  upstream `FileHash`).
