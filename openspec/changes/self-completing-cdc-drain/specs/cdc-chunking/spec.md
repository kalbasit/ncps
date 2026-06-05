## ADDED Requirements

### Requirement: A de-chunked NAR's narinfo MUST be consistent with its whole-file storage

Once a NAR has been converted from chunks to whole-file (`Compression:none`) storage, the persisted narinfo for that NAR SHALL advertise the Compression:none URL (`nar/<H>.nar`) with FileHash null, matching the actual storage. The narinfo SHALL NOT be left advertising a chunk-era or different-compression URL whose servability depended on `maybeCDCNormalizeNarInfoURL` (which only rewrites while the NAR is still chunked).

#### Scenario: Serving a de-chunked NAR does not depend on serve-time chunk normalization

- **GIVEN** a NAR that has been de-chunked to `none/whole` storage
- **AND** its narinfo advertised `nar/<H>.nar.xz` before de-chunking
- **WHEN** the narinfo is served after de-chunking (the NAR is no longer chunked, so `HasNarInChunks` is false)
- **THEN** the served narinfo SHALL advertise `nar/<H>.nar` (Compression none)
- **AND** a GET of that URL SHALL return the whole NAR, not 404
