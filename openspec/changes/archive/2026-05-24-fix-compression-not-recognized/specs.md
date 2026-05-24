## Specifications

### Spec 1: GetNarInfo normalizes narinfo URL when NAR is already chunked in CDC mode

**Given** CDC is enabled
**And** a narinfo exists in the DB with `Compression: xz` / URL `nar/hash.nar.xz`
**And** the NAR has been migrated to CDC chunks (`HasNarInChunks` returns true)
**When** `GetNarInfo` is called for that hash
**Then** the returned narinfo has `Compression: none` and URL `nar/hash.nar`
**And** FileHash is nil and FileSize is 0
**And** the narinfo DB record is NOT modified synchronously (update is async)

### Spec 2: GetNarInfo does NOT normalize when NAR is not yet chunked

**Given** CDC is enabled
**And** a narinfo exists in the DB with `Compression: xz` / URL `nar/hash.nar.xz`
**And** the NAR is NOT in CDC chunks (`HasNarInChunks` returns false)
**When** `GetNarInfo` is called for that hash
**Then** the returned narinfo retains `Compression: xz` and the original URL
**And** background migration is triggered

### Spec 3: No normalization when CDC is disabled

**Given** CDC is disabled
**And** a narinfo exists in the DB with `Compression: xz`
**When** `GetNarInfo` is called
**Then** the returned narinfo retains the original `Compression: xz`
