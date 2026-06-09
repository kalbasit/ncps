## ADDED Requirements

### Requirement: Predictive narinfo none steers eager-CDC cross-pod readers to the uncompressed staging serve

When eager CDC is active, ncps advertises the narinfo as `Compression: none` (see the `cdc-chunking` capability), so a cross-pod reader requests the uncompressed `.nar` variant during the chunking window. The uncompressed request SHALL be satisfied from staging (or progressive chunks) per the existing cross-pod staging requirement. The compressed-variant upstream fallback SHALL remain ONLY as a defensive backstop for a directly-constructed `.nar.xz` request (e.g., a client holding a stale `xz` narinfo). Under eager CDC the common cross-pod path SHALL NOT exercise the upstream compressed fallback, because no client requests `.nar.xz`.

#### Scenario: Eager-CDC cross-pod reader fetches narinfo then serves .nar from staging

- **WHEN** eager CDC is active, replica A is actively chunking a NAR, and replica B fetches the narinfo for that hash
- **THEN** the narinfo advertises `Compression: none` / `.nar`
- **AND** replica B requests the uncompressed `.nar` and serves it from staging with HTTP 200
- **AND** replica B does NOT request `.nar.xz` and does NOT fall back to upstream

#### Scenario: Stale xz narinfo still falls back defensively

- **WHEN** a client holds a stale narinfo advertising `Compression: xz` for an eager-CDC NAR that exists only as uncompressed in-flight bytes
- **AND** it requests `.nar.xz` cross-pod during the chunking window
- **THEN** the staging serve returns not-found and the client falls back to an upstream that has the original compressed file
- **AND** the uncompressed staged bytes are NOT served mislabeled as `xz`
