## 1. Schema + migration (data-model)

- [x] 1.1 Add nullable `dechunk_residue_flagged_at` timestamp to the `nar_file` Ent schema; regenerate the client
- [x] 1.2 Generate forward-only per-dialect migration `add_dechunk_residue_flagged_at_to_nar_files`; rehash atlas.sum

## 2. fsck residue detection + repair (fsck)

- [x] 2.1 Per chunked `nar_file`, classify via the de-chunk resolver: recoverable (resolvable NarHash) vs un-de-chunkable
- [x] 2.2 Recoverable: relink + normalize the narinfo URL to none immediately; clear any residue flag
- [x] 2.3 Un-de-chunkable: set `dechunk_residue_flagged_at` if unset; if flagged older than the (configurable, default ~24h) grace window and still un-de-chunkable, purge via `PurgeChunkedNar`; skip rows with a recent `chunking_started_at`
- [x] 2.4 Became-recoverable / de-chunked rows: clear the flag

## 3. Tests

- [x] 3.1 Recoverable inconsistent chunked NAR is normalized, not purged
- [x] 3.2 First detection flags but does not purge
- [x] 3.3 Aged + still-un-de-chunkable row is purged on a later run
- [x] 3.4 A row that became recoverable is unflagged, never purged
- [x] 3.5 A row with a recent `chunking_started_at` is not flagged/purged

## 4. Verification

- [x] 4.1 `task ent:check`, `task fmt`, `task lint`, `task test` pass
- [x] 4.2 Deploy; run de-chunk + fsck against the real production residue; confirm fsck flags then reclaims across two runs and de-chunk + fsck together leave a clean, consistent state
- [x] 4.3 CDC-lifecycle e2e test exercises the fsck reclaimer alongside the drain
