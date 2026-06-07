-- +goose Up
-- Repair narinfos stranded by the removed store-time eager-CDC URL normalization
-- (see pkg/cache/cache.go pullNarInfo). Such a narinfo advertises
-- url=nar/<hash>.nar (Compression none) while its ONLY backing nar_file is a
-- whole-file xz NAR (compression=xz, total_chunks=0). The none URL 404s because
-- the bytes live only at /nar/<hash>.nar.xz, which aborts `nix copy` reference
-- checks. Restore the narinfo to match actual storage (xz url/compression/
-- file_hash/file_size), all reconstructed from the joined nar_file (nf.hash is the
-- xz file hash). Narinfos that also have a servable none/chunked backing are
-- excluded (already correct). Idempotent: after repair the url ends in .nar.xz and
-- no longer matches the predicate.
UPDATE narinfos AS ni
SET url = 'nar/' || nf.hash || '.nar.xz',
    compression = 'xz',
    file_hash = 'sha256:' || nf.hash,
    file_size = nf.file_size,
    updated_at = now()
FROM narinfo_nar_files AS l
INNER JOIN nar_files AS nf ON nf.id = l.nar_file_id
WHERE l.narinfo_id = ni.id
  AND ni.url LIKE '%.nar'
  AND nf.compression = 'xz'
  AND nf.total_chunks = 0
  AND NOT EXISTS (
    SELECT 1
    FROM narinfo_nar_files AS l2
    INNER JOIN nar_files AS nf2 ON nf2.id = l2.nar_file_id
    WHERE l2.narinfo_id = ni.id
      AND (nf2.compression <> 'xz' OR nf2.total_chunks > 0)
  );

-- +goose Down
-- Forward-only data repair: the pre-repair state was a corruption, not a target to
-- restore. No automatic rollback.
