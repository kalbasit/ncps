## 1. Schema + migration

- [x] 1.1 Add `nar_file.bytes_stored_at` (Optional, Nillable) to the Ent schema; regenerate client
- [x] 1.2 Generate forward-only per-dialect migration `add_bytes_stored_at_to_nar_files`; rehash atlas.sum

## 2. Marker write

- [x] 2.1 `PutNar` sets `bytes_stored_at` once the NAR's bytes are durably written (create + upsert paths)

## 3. Upload-path presence (upload-reference-presence)

- [x] 3.1 `narFileBytesStored` matches `nar_file` by hash+query, any compression, `bytes_stored_at NOT NULL`
- [x] 3.2 The upload-only narinfo read consults the marker so a peer-stored / compression-mismatched NAR is reported present
- [x] 3.3 Tests: marker trusted on upload path; compression-agnostic match; byte-less placeholder not trusted; substituter path still self-heals

## 4. Verification

- [x] 4.1 `task fmt`, `task lint`, `task test` pass
- [x] 4.2 Apply the migration + backfill in production; confirm previously-failing references report present (HTTP 200) on `/upload`
