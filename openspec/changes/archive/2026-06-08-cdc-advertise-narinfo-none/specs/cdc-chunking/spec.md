## MODIFIED Requirements

### Requirement: GetNarInfo MUST normalize compression in-memory during lazy-chunking transition

When CDC is enabled, `GetNarInfo` MUST normalize the narinfo compression in-memory before returning so clients receive a consistent advertised representation without waiting for an asynchronous DB update. The normalization behavior depends on the CDC mode:

- **Lazy chunking** (the whole upstream-compressed file is retained until background migration completes): narinfos are initially stored with the upstream compression (e.g., `Compression: xz`). `GetNarInfo` SHALL normalize to `Compression: none` / `.nar` ONLY once the NAR is genuinely chunked, gated on `HasNarInChunks` (a lightweight indexed query checking `total_chunks > 0`). Until then the whole `xz` file remains servable and the narinfo retains its upstream compression.
- **Eager chunking** (no retained whole-file; the durable form is uncompressed chunks): `GetNarInfo` SHALL normalize to `Compression: none` / `.nar` **predictively**, regardless of `HasNarInChunks`, because a `.nar.xz` request cannot be reliably served during the eager chunking window. See the requirement "Eager CDC MUST advertise narinfo Compression none predictively."

The normalization SHALL:
- Rewrite `Compression`, `URL`, `FileHash`, and `FileSize` in memory only — the DB record is NOT modified synchronously
- Normalize the URL hash before any `nar_file` lookup to handle nix-serve-style prefixed hashes (e.g., `abc-hash` → `hash`) matching nar_file rows correctly
- Skip normalization entirely when CDC is disabled

#### Scenario: Lazy chunking normalizes when NAR is already chunked

- **GIVEN** CDC is enabled with lazy chunking
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR has been migrated to CDC chunks (`HasNarInChunks` returns true)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo has `Compression: none` and URL `nar/hash.nar`
- **AND** `FileHash` is nil and `FileSize` is 0
- **AND** the narinfo DB record is NOT modified synchronously

#### Scenario: Lazy chunking does NOT normalize when NAR is not yet chunked

- **GIVEN** CDC is enabled with lazy chunking
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR is NOT in CDC chunks (`HasNarInChunks` returns false)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo retains `Compression: xz` and the original URL
- **AND** background migration is triggered

#### Scenario: Eager chunking normalizes predictively before chunks exist

- **GIVEN** CDC is enabled with eager chunking (lazy chunking disabled)
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz` (a legacy row predating predictive store-time normalization)
- **AND** the NAR is NOT in CDC chunks (`HasNarInChunks` returns false)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo has `Compression: none` and URL `nar/hash.nar`
- **AND** `FileHash` is nil and `FileSize` is 0

#### Scenario: No normalization when CDC is disabled

- **GIVEN** CDC is disabled
- **AND** a narinfo exists in the DB with `Compression: xz`
- **WHEN** `GetNarInfo` is called
- **THEN** the returned narinfo retains the original `Compression: xz`

## ADDED Requirements

### Requirement: Eager CDC MUST advertise narinfo Compression none predictively

When eager CDC is active (`isCDCEnabled` is true AND lazy chunking is disabled), ncps MUST advertise every CDC NAR's narinfo as `Compression: none` / `.nar` consistently — at narinfo store time on the pull path AND at serve time — so clients always request the uncompressed `.nar` and never `.nar.xz`. The eager durable form is uncompressed chunks; ncps has no NAR compressor and a re-compressed file would not match the narinfo `FileHash`/`FileSize`, so a `.nar.xz` request cannot be reliably served during the chunking window. This brings the pull path into parity with the upload path (`PutNarInfo`), which already normalizes CDC narinfos to `none`.

The normalization SHALL:
- On the pull path, persist `Compression: none`, URL `nar/<hash>.nar`, and null `FileHash`/`FileSize` when storing the narinfo, mirroring `PutNarInfo`.
- Be gated strictly on eager CDC. Lazy-mode narinfos SHALL retain their upstream compression (the whole `xz` file remains servable as `.nar.xz`).
- Leave non-CDC and CDC-disabled narinfos unchanged.

A `.nar` request for a predictively-normalized NAR whose bytes are not yet materialized (chunking not started, process restart, or eviction) SHALL be routed by `narServability` to an upstream (re-)download and SHALL NOT return a terminal `storage.ErrNotFound`.

#### Scenario: Pull-path store advertises none for eager CDC

- **GIVEN** eager CDC is active (lazy chunking disabled)
- **AND** a narinfo is fetched from upstream advertising `Compression: xz` with URL `nar/hash.nar.xz`
- **WHEN** the narinfo is stored on the pull path
- **THEN** the persisted narinfo advertises `Compression: none` and URL `nar/hash.nar`
- **AND** `FileHash` is null and `FileSize` is 0

#### Scenario: Cold client receives none before any nar_file row exists

- **GIVEN** eager CDC is active
- **AND** a client requests the narinfo for a hash that has never been fetched (no `nar_file` row exists)
- **WHEN** ncps fetches, stores, and returns the narinfo
- **THEN** the returned narinfo advertises `Compression: none` / `nar/hash.nar`
- **AND** the client's subsequent NAR request is for the uncompressed `.nar`

#### Scenario: Predictive none with unmaterialized bytes triggers re-download, not 404

- **GIVEN** eager CDC is active
- **AND** a narinfo advertises `Compression: none` for a hash whose chunks and whole-file are absent (no servable `nar_file`)
- **WHEN** `GetNar` is called for `nar/hash.nar`
- **THEN** `narServability` reports the NAR not servable
- **AND** the request triggers an upstream (re-)download rather than returning `storage.ErrNotFound`

#### Scenario: Lazy CDC narinfo is NOT predictively normalized

- **GIVEN** CDC is enabled with lazy chunking
- **AND** a narinfo is fetched from upstream advertising `Compression: xz`
- **WHEN** the narinfo is stored on the pull path
- **THEN** the persisted narinfo retains `Compression: xz` and its `.nar.xz` URL
- **AND** the whole `xz` file remains servable as `.nar.xz`
