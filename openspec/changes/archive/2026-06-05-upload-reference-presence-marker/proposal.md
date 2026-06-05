## Why

`nix copy --to .../upload` verifies each reference by fetching its narinfo and aborts the whole copy if one 404s. Two production conditions made a present NAR's narinfo 404 on the `/upload` path:

1. **Multi-replica / shared NFS lag**: a NAR durably written by one replica is recorded in the shared database before another replica's local filesystem `stat` observes the NFS write. The second replica's reference check found no local bytes and treated the NAR as absent.
2. **CDC compression residue**: some narinfos advertise `url=nar/<hash>.nar` (Compression none) while the NAR is durably stored only under another compression (xz). A presence check keyed on the narinfo URL's compression missed the stored row.

Both surface as `cannot add X because the reference Y does not exist`, aborting otherwise-valid uploads.

## What Changes

- Add a dedicated `nar_file.bytes_stored_at` column (distinct from fsck's `verified_at`) that `PutNar` sets once a NAR's bytes are durably written.
- On the `/upload` (upload-only) path, treat a NAR as present when a `nar_file` matching its hash and query — **regardless of compression** — has `bytes_stored_at` set, trusting the shared-database marker over this replica's local `stat`.
- A byte-less placeholder row (e.g. created by a narinfo PUT, `bytes_stored_at` NULL) is **not** trusted, so phantoms are not resurrected.
- The marker is trusted only on the `/upload` path; the substituter path keeps self-healing a genuinely missing NAR from upstream.

## Capabilities

### New Capabilities

- `upload-reference-presence`: the rules by which the `/upload` path decides a narinfo's backing NAR is present, so a `nix copy` reference check does not 404 a durably-stored NAR.

### Modified Capabilities

(none)

## Impact

- `ent/schema/nar_file.go` + `migrations/{sqlite,postgres,mysql}/*_add_bytes_stored_at_to_nar_files.sql`: new nullable column.
- `pkg/cache/cache.go`: `PutNar` sets `bytes_stored_at`; `narFileBytesStored` (compression-agnostic, marker-gated) consulted by the upload-only narinfo read.
