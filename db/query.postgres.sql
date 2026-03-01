-- name: GetConfigByKey :one
SELECT *
FROM config
WHERE key = $1;

-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = $1;

-- name: GetNarInfoHashByNarURL :one
SELECT hash
FROM narinfos
WHERE url = $1
LIMIT 1;

-- name: GetNarFileByHashAndCompressionAndQuery :one
SELECT id, hash, compression, file_size, query, created_at, updated_at, last_accessed_at, total_chunks, chunking_started_at, verified_at
FROM nar_files
WHERE hash = $1 AND compression = $2 AND query = $3;

-- name: GetNarFileByNarInfoID :one
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.query, nf.created_at, nf.updated_at, nf.last_accessed_at, nf.total_chunks, nf.chunking_started_at, nf.verified_at
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
WHERE nnf.narinfo_id = $1;

-- name: GetNarInfoURLByNarFileHash :one
SELECT ni.url
FROM narinfos ni
INNER JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
INNER JOIN nar_files nf ON nf.id = nnf.nar_file_id
WHERE nf.hash = $1 AND nf.compression = $2 AND nf.query = $3
LIMIT 1;

-- name: CreateConfig :one
INSERT INTO config (
    key, value
) VALUES (
    $1, $2
)
RETURNING *;

-- name: SetConfig :exec
INSERT INTO config (
    key, value
) VALUES (
    $1, $2
)
ON CONFLICT(key)
DO UPDATE SET
  value = EXCLUDED.value,
  updated_at = CURRENT_TIMESTAMP;

-- name: CreateNarInfo :one
INSERT INTO narinfos (
    hash, store_path, url, compression, file_hash, file_size, nar_hash, nar_size, deriver, system, ca
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
ON CONFLICT (hash) DO UPDATE SET
    store_path = EXCLUDED.store_path,
    url = EXCLUDED.url,
    compression = EXCLUDED.compression,
    file_hash = EXCLUDED.file_hash,
    file_size = EXCLUDED.file_size,
    nar_hash = EXCLUDED.nar_hash,
    nar_size = EXCLUDED.nar_size,
    deriver = EXCLUDED.deriver,
    system = EXCLUDED.system,
    ca = EXCLUDED.ca,
    updated_at = CURRENT_TIMESTAMP
WHERE narinfos.url IS NULL
RETURNING *;

-- name: UpdateNarInfo :one
UPDATE narinfos
SET
    store_path = $2,
    url = $3,
    compression = $4,
    file_hash = $5,
    file_size = $6,
    nar_hash = $7,
    nar_size = $8,
    deriver = $9,
    system = $10,
    ca = $11,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = $1
RETURNING *;

-- name: UpdateNarInfoFileSize :exec
UPDATE narinfos
SET file_size = $2, updated_at = CURRENT_TIMESTAMP
WHERE hash = $1;

-- name: UpdateNarInfoFileHash :exec
UPDATE narinfos
SET file_hash = $2, updated_at = CURRENT_TIMESTAMP
WHERE hash = $1;

-- name: AddNarInfoReference :exec
INSERT INTO narinfo_references (
    narinfo_id, reference
) VALUES (
    $1, $2
)
ON CONFLICT (narinfo_id, reference) DO NOTHING;

-- @bulk-for AddNarInfoReference
-- name: AddNarInfoReferences :exec
INSERT INTO narinfo_references (
    narinfo_id, reference
)
SELECT $1, unnest(sqlc.arg(reference)::text[]) ON CONFLICT (narinfo_id, reference) DO NOTHING;

-- name: AddNarInfoSignature :exec
INSERT INTO narinfo_signatures (
    narinfo_id, signature
) VALUES (
    $1, $2
)
ON CONFLICT (narinfo_id, signature) DO NOTHING;

-- @bulk-for AddNarInfoSignature
-- name: AddNarInfoSignatures :exec
INSERT INTO narinfo_signatures (
    narinfo_id, signature
)
SELECT $1, unnest(sqlc.arg(signature)::text[]) ON CONFLICT (narinfo_id, signature) DO NOTHING;

-- name: GetNarInfoReferences :many
SELECT reference
FROM narinfo_references
WHERE narinfo_id = $1;

-- name: GetNarInfoSignatures :many
SELECT signature
FROM narinfo_signatures
WHERE narinfo_id = $1;

-- name: GetNarInfoHashesByURL :many
SELECT hash
FROM narinfos
WHERE url = $1;

-- name: CreateNarFile :one
INSERT INTO nar_files (
    hash, compression, query, file_size, total_chunks
) VALUES (
    $1, $2, $3, $4, $5
)
ON CONFLICT (hash, compression, query) DO UPDATE SET
    updated_at = EXCLUDED.updated_at
RETURNING
    id,
    hash,
    compression,
    file_size,
    query,
    created_at,
    updated_at,
    last_accessed_at,
    total_chunks,
    chunking_started_at,
    verified_at;

-- name: SetNarFileChunkingStarted :exec
UPDATE nar_files
SET chunking_started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE id = $1;

-- name: LinkNarInfoToNarFile :exec
INSERT INTO narinfo_nar_files (
    narinfo_id, nar_file_id
) VALUES (
    $1, $2
)
ON CONFLICT (narinfo_id, nar_file_id) DO NOTHING;

-- name: LinkNarInfosByURLToNarFile :exec
INSERT INTO narinfo_nar_files (narinfo_id, nar_file_id)
SELECT id, $1
FROM narinfos
WHERE url = $2
ON CONFLICT (narinfo_id, nar_file_id) DO NOTHING;

-- name: TouchNarInfo :execrows
UPDATE narinfos
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = $1;

-- name: TouchNarFile :execrows
UPDATE nar_files
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = $1 AND compression = $2 AND query = $3;

-- name: DeleteNarInfoByHash :execrows
DELETE FROM narinfos
WHERE hash = $1;

-- name: DeleteNarFileByHash :execrows
DELETE FROM nar_files
WHERE hash = $1 AND compression = $2 AND query = $3;

-- name: DeleteNarInfoByID :execrows
DELETE FROM narinfos
WHERE id = $1;

-- name: UpdateNarFileFileSize :exec
UPDATE nar_files
SET file_size = $1, updated_at = CURRENT_TIMESTAMP
WHERE id = $2;

-- name: DeleteOrphanedNarFiles :execrows
DELETE FROM nar_files
WHERE id NOT IN (
    SELECT DISTINCT nar_file_id
    FROM narinfo_nar_files
);

-- name: GetNarTotalSize :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_size
FROM nar_files;

-- name: GetNarInfoCount :one
SELECT CAST(COUNT(*) AS BIGINT) AS count
FROM narinfos;

-- name: GetNarFileCount :one
SELECT CAST(COUNT(*) AS BIGINT) AS count
FROM nar_files;

-- name: GetLeastUsedNarInfos :many
-- NOTE: This query uses a correlated subquery which is not optimal for performance.
-- The ideal implementation would use a window function (SUM OVER), but sqlc v1.30.0
-- does not properly support filtering on window function results in subqueries.
-- Gets the least-used narinfos up to a certain total file size (accounting for their nar_files).
SELECT ni1.*
FROM narinfos ni1
WHERE (
    SELECT COALESCE(SUM(nf.file_size), 0)
    FROM nar_files nf
    WHERE nf.id IN (
        SELECT DISTINCT nnf.nar_file_id
        FROM narinfo_nar_files nnf
        INNER JOIN narinfos ni2 ON nnf.narinfo_id = ni2.id
        WHERE ni2.last_accessed_at < ni1.last_accessed_at
            OR (ni2.last_accessed_at = ni1.last_accessed_at AND ni2.id <= ni1.id)
    )
) <= $1;

-- name: GetOrphanedNarFiles :many
-- Find files that have no relationship to any narinfo
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.query, nf.created_at, nf.updated_at, nf.last_accessed_at, nf.total_chunks, nf.chunking_started_at, nf.verified_at
FROM nar_files nf
LEFT JOIN narinfo_nar_files ninf ON nf.id = ninf.nar_file_id
WHERE ninf.narinfo_id IS NULL;

-- name: GetUnmigratedNarInfoHashes :many
-- Get all narinfo hashes that have no URL (unmigrated).
SELECT hash
FROM narinfos
WHERE url IS NULL;

-- name: GetMigratedNarInfoHashes :many
-- Get all narinfo hashes that have a URL (migrated).
SELECT hash
FROM narinfos
WHERE url IS NOT NULL;

-- name: GetChunkByHash :one
SELECT *
FROM chunks
WHERE hash = $1;

-- name: GetChunksByNarFileID :many
SELECT c.id, c.hash, c.size, c.compressed_size, c.created_at, c.updated_at
FROM chunks c
INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.nar_file_id = $1
ORDER BY nfc.chunk_index;

-- name: CreateChunk :one
INSERT INTO chunks (
    hash, size, compressed_size
) VALUES (
    $1, $2, $3
)
ON CONFLICT(hash) DO UPDATE SET
    updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: LinkNarFileToChunk :exec
INSERT INTO nar_file_chunks (
    nar_file_id, chunk_id, chunk_index
) VALUES (
    $1, $2, $3
)
ON CONFLICT (nar_file_id, chunk_index) DO NOTHING;

-- @bulk-for LinkNarFileToChunk
-- name: LinkNarFileToChunks :exec
INSERT INTO nar_file_chunks (
    nar_file_id, chunk_id, chunk_index
)
SELECT $1, unnest(sqlc.arg(chunk_id)::bigint[]), unnest(sqlc.arg(chunk_index)::bigint[])
ON CONFLICT (nar_file_id, chunk_index) DO NOTHING;

-- name: GetChunkCount :one
SELECT CAST(COUNT(*) AS BIGINT) AS count
FROM chunks;

-- name: GetOrphanedChunks :many
SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
FROM chunks c
LEFT JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.chunk_id IS NULL;

-- name: DeleteOrphanedChunks :execrows
DELETE FROM chunks
WHERE NOT EXISTS (
    SELECT 1
    FROM nar_file_chunks
    WHERE chunk_id = chunks.id
);

-- name: DeleteChunkByID :exec
DELETE FROM chunks
WHERE id = $1;

-- name: DeleteNarFileChunksByNarFileID :exec
DELETE FROM nar_file_chunks
WHERE nar_file_id = $1;

-- name: GetChunkByNarFileIDAndIndex :one
SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
FROM chunks c
INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.nar_file_id = $1 AND nfc.chunk_index = $2;

-- name: UpdateNarFileTotalChunks :exec
UPDATE nar_files
SET total_chunks = $1, file_size = $2, updated_at = CURRENT_TIMESTAMP, chunking_started_at = NULL
WHERE id = $3;

-- name: UpdateNarFileVerifiedAt :exec
UPDATE nar_files
SET verified_at = CURRENT_TIMESTAMP
WHERE id = $1;

-- name: GetNarFilesToChunk :many
-- Get all NAR files that are not yet chunked.
SELECT id, hash, compression, query, file_size
FROM nar_files
WHERE total_chunks = 0
ORDER BY id;

-- name: GetNarFilesToChunkCount :one
-- Get the count of NAR files that are not yet chunked.
SELECT COUNT(*)
FROM nar_files
WHERE total_chunks = 0;

-- name: UpdateNarInfoCompressionAndURL :execrows
-- Update narinfo compression and URL after CDC migration.
UPDATE narinfos
SET compression = sqlc.arg(compression), url = sqlc.arg(new_url), updated_at = CURRENT_TIMESTAMP
WHERE url = sqlc.arg(old_url);

-- name: UpdateNarInfoCompressionFileSizeHashAndURL :execrows
-- Update narinfo compression, file_size, file_hash and URL after CDC migration.
UPDATE narinfos
SET
    compression = sqlc.arg(compression),
    url = sqlc.arg(new_url),
    file_size = sqlc.arg(file_size),
    file_hash = sqlc.arg(file_hash),
    updated_at = CURRENT_TIMESTAMP
WHERE url = sqlc.arg(old_url);

-- name: GetAllNarFiles :many
-- Returns all nar_files for storage existence verification.
SELECT id, hash, compression, query, file_size, total_chunks, chunking_started_at, created_at, updated_at, last_accessed_at, verified_at
FROM nar_files;

-- name: GetNarInfosWithoutNarFiles :many
-- Returns narinfos that have no linked nar_file entries.
SELECT ni.*
FROM narinfos ni
WHERE NOT EXISTS (
    SELECT 1 FROM narinfo_nar_files nnf WHERE nnf.narinfo_id = ni.id
);

-- name: GetAllChunks :many
-- Returns all chunks for storage existence verification (CDC mode).
SELECT id, hash, size, compressed_size, created_at, updated_at
FROM chunks;

-- name: HasAnyChunkedNarFiles :one
-- Returns true if any nar_file has total_chunks > 0 (used for CDC auto-detection).
SELECT EXISTS(SELECT 1 FROM nar_files WHERE total_chunks > 0) AS "exists";
