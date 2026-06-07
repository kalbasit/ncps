## ADDED Requirements

### Requirement: GetNar MUST advertise the compression of the bytes it serves

When `GetNar` serves a NAR from the whole-file store (not from chunks), the compression it reports to the caller MUST describe the bytes actually streamed. The serve path MUST NOT relabel the served bytes using a CDC-first database lookup that may return a different representation's compression (e.g. a chunked `compression=none` row that coexists with the whole file during a lazy-chunking transition). Concretely, the response compression MUST equal the compression of the file actually opened, after accounting for transparent zstd→none decompression.

This is the NAR-serve counterpart to "GetNarInfo MUST normalize compression in-memory during lazy-chunking transition": together they keep the narinfo's advertised `Compression`/`URL`, the NAR response's compression, and the streamed bytes mutually consistent so `nix` never sees a size shortfall or an unrecognized compression.

#### Scenario: Whole-file xz NAR served while a chunked none row coexists

- **WHEN** CDC is enabled, a NAR exists as a whole xz file AND a chunked `compression=none` row for the same hash coexists (the lazy-chunking transition window), and a client requests the xz NAR
- **THEN** `GetNar` streams the xz file bytes and reports `Compression: xz` (not `none`), with a byte count equal to the xz file size, so the client decompresses to exactly `NarSize`

#### Scenario: Whole-file none NAR served transparently from zstd

- **WHEN** a client requests an uncompressed (`none`) NAR that is physically stored as `.nar.zst` and there is no chunked representation
- **THEN** `GetNar` transparently decompresses and reports `Compression: none`, matching the uncompressed bytes it streams

#### Scenario: Enable-CDC-on-existing-cache upgrade path serves correctly

- **WHEN** a cache that already holds whole-file (xz) NARs has CDC enabled and a client fetches a closure whose paths include those pre-existing whole-file NARs
- **THEN** every such NAR is served with a compression label matching its bytes, and `nix` completes the fetch without `NAR ... is incomplete` or `input compression not recognized`
