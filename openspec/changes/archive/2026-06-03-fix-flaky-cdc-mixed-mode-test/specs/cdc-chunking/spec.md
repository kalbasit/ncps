## ADDED Requirements

### Requirement: Serving a whole-file NAR MUST be resilient to a concurrent background migration

The system SHALL serve a NAR that it observed as present even if a concurrent background
NAR→chunks migration removes the whole file mid-serve.

When `GetNar` observes a NAR present as a whole file in the store while CDC is enabled, it
MAY trigger a background NAR→chunks migration that, on completion, deletes the whole file
from the store. The synchronous serve that follows MUST NOT surface
`storage.ErrNotFound` to the caller solely because that background migration removed the
whole file between the time-of-check (`HasNarInStore`) and the time-of-use
(`getNarFromStore`).

The system SHALL treat a whole-file store read that misses, while CDC is enabled and the
request is for the uncompressed NAR (`Compression == none`), as a signal that the NAR has
been migrated, and SHALL fall back to serving the NAR by reassembling it from chunks. Only
when neither the whole file nor a reassemblable set of chunks exists MAY `GetNar` return
`storage.ErrNotFound`.

This fallback applies exclusively to the uncompressed serve path. A request for a
compressed NAR (e.g. `.nar.xz`) whose whole file is gone MUST continue to resolve to
`storage.ErrNotFound`, because chunks are stored uncompressed and cannot reconstruct a
compressed stream (consistent with the existing chunked-serving rules).

#### Scenario: Whole-file deleted by background migration mid-serve falls back to chunks

- **GIVEN** a NAR for hash `H` stored as a whole file while CDC was disabled
- **AND** CDC is subsequently enabled
- **WHEN** `GetNar(H)` is called and the triggered background migration deletes the whole
  file from the store before the synchronous `getNarFromStore` opens it
- **THEN** the serve path detects the store miss and reassembles `H` from its chunks
- **AND** `GetNar` returns the complete NAR bytes with no error
- **AND** `storage.ErrNotFound` is NOT surfaced to the caller

#### Scenario: Mixed-mode retrieval after enabling CDC always succeeds

- **GIVEN** a blob NAR stored with CDC disabled and a separate NAR stored with CDC enabled
- **WHEN** both NARs are retrieved via `GetNar` after CDC is enabled
- **THEN** each retrieval returns its exact original content
- **AND** neither retrieval fails with `error fetching the nar from the store: not found`,
  regardless of background-migration timing

#### Scenario: Genuinely absent NAR still returns not found

- **GIVEN** a hash `H` that has neither a whole file in the store nor any chunks
- **WHEN** `GetNar(H)` is called in upload-only mode (no upstream pull)
- **THEN** `GetNar` returns `storage.ErrNotFound`

#### Scenario: Compressed request after migration still returns not found

- **GIVEN** a NAR whose whole file has been migrated to (uncompressed) chunks
- **WHEN** a request for the compressed NAR (`Compression != none`) is served
- **THEN** the serve path returns `storage.ErrNotFound` rather than serving raw chunk bytes
