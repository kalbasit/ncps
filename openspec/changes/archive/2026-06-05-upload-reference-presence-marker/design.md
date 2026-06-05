## Context

Production runs ncps with 2 replicas on a shared NFS RWX local backend and a shared external Postgres. The `/upload` path (a workaround for NixOS/nix#15249 so push reference queries 404 instead of pull-through) checks whether a narinfo's backing NAR is present before serving the narinfo. The original check relied on the local filesystem (`HasNarInStore`/`HasNarInChunks`), which lags the shared database across replicas and is keyed on the narinfo URL's compression.

## Goals / Non-Goals

- **Goal**: a reference check on the upload path reports a NAR present whenever it is durably stored anywhere the cluster can see, regardless of which replica wrote it or which compression it is stored under.
- **Non-Goal**: change substituter-path behavior. There a missing local NAR must still self-heal from upstream; trusting a shared marker there could mask a real eviction.

## Decisions

- **Dedicated `bytes_stored_at` column, not `verified_at`.** `verified_at` is owned by fsck (it gates `shouldCheckNar`); `PutNar` does not hash-verify, so overloading `verified_at` would be semantically wrong and would perturb fsck scheduling. A separate nullable column records "bytes durably written".
- **Compression-agnostic lookup.** `narFileBytesStored` matches `nar_file` by hash+query with `bytes_stored_at NOT NULL`, any compression. The hash identifies the NAR; the narinfo URL may advertise a different compression (CDC residue) than the stored row.
- **Upload-path only.** The marker is consulted only when `IsUploadOnly(ctx)` is true.
- **Placeholders excluded.** Only a row with `bytes_stored_at` set is trusted; a narinfo-PUT placeholder (marker NULL) is not, preserving the phantom-safety guarantees of the surrounding non-destructive-purge work.

## Risks / Trade-offs

- The marker reflects "bytes were written", not "bytes are currently present" — an eviction could clear the bytes without clearing the marker. This is acceptable on the upload path (the worst case is a reference reported present that a later GET re-pulls), and is explicitly not trusted on the substituter path.
