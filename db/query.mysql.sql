-- name: GetConfigByID :one
SELECT *
FROM config
WHERE id = ?;

-- name: GetConfigByKey :one
SELECT *
FROM config
WHERE `key` = ?;

-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = ?;

-- name: GetNarInfoByID :one
SELECT *
FROM narinfos
WHERE id = ?;


-- name: GetNarFileByHashAndCompressionAndQuery :one
SELECT id, hash, compression, file_size, `query`, created_at, updated_at, last_accessed_at, total_chunks, chunking_started_at
FROM nar_files
WHERE hash = ? AND compression = ? AND `query` = ?;

-- name: GetNarFileByID :one
SELECT id, hash, compression, file_size, `query`, created_at, updated_at, last_accessed_at, total_chunks, chunking_started_at
FROM nar_files
WHERE id = ?;

-- name: GetNarFileByNarInfoID :one
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.`query`, nf.created_at, nf.updated_at, nf.last_accessed_at, nf.total_chunks, nf.chunking_started_at
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
WHERE nnf.narinfo_id = ?;

-- name: GetNarInfoHashesByNarFileID :many
SELECT ni.hash
FROM narinfos ni
INNER JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
WHERE nnf.nar_file_id = ?;

-- name: GetNarInfoURLByNarFileHash :one
SELECT ni.url
FROM narinfos ni
INNER JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
INNER JOIN nar_files nf ON nf.id = nnf.nar_file_id
WHERE nf.hash = ? AND nf.compression = ? AND nf.query = ?
LIMIT 1;

-- name: CreateConfig :execresult
INSERT INTO config (
    `key`, value
) VALUES (
    ?, ?
);

-- name: SetConfig :exec
INSERT INTO config (
    `key`, value
) VALUES (
    ?, ?
)
ON DUPLICATE KEY UPDATE
    value = VALUES(value),
    updated_at = CURRENT_TIMESTAMP;

-- name: CreateNarInfo :execresult
INSERT INTO narinfos (
    hash, store_path, url, compression, file_hash, file_size, nar_hash, nar_size, deriver, system, ca
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
ON DUPLICATE KEY UPDATE
    id = LAST_INSERT_ID(id),
    store_path = IF(url IS NULL, VALUES(store_path), store_path),
    compression = IF(url IS NULL, VALUES(compression), compression),
    file_hash = IF(url IS NULL, VALUES(file_hash), file_hash),
    file_size = IF(url IS NULL, VALUES(file_size), file_size),
    nar_hash = IF(url IS NULL, VALUES(nar_hash), nar_hash),
    nar_size = IF(url IS NULL, VALUES(nar_size), nar_size),
    deriver = IF(url IS NULL, VALUES(deriver), deriver),
    system = IF(url IS NULL, VALUES(system), system),
    ca = IF(url IS NULL, VALUES(ca), ca),
    url = IF(url IS NULL, VALUES(url), url),
    updated_at = IF(url IS NULL, CURRENT_TIMESTAMP, updated_at);

-- name: UpdateNarInfo :execresult
UPDATE narinfos
SET
    store_path = ?,
    url = ?,
    compression = ?,
    file_hash = ?,
    file_size = ?,
    nar_hash = ?,
    nar_size = ?,
    deriver = ?,
    system = ?,
    ca = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: UpdateNarInfoFileSize :exec
UPDATE narinfos
SET file_size = ?, updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: UpdateNarInfoFileHash :exec
UPDATE narinfos
SET file_hash = ?, updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: AddNarInfoReference :exec
INSERT IGNORE INTO narinfo_references (
    narinfo_id, reference
) VALUES (
    ?, ?
);

-- name: AddNarInfoSignature :exec
INSERT IGNORE INTO narinfo_signatures (
    narinfo_id, signature
) VALUES (
    ?, ?
);

-- name: GetNarInfoReferences :many
SELECT reference
FROM narinfo_references
WHERE narinfo_id = ?;

-- name: GetNarInfoSignatures :many
SELECT signature
FROM narinfo_signatures
WHERE narinfo_id = ?;

-- name: GetNarInfoHashesByURL :many
SELECT hash
FROM narinfos
WHERE url = ?;

-- name: CreateNarFile :execresult
INSERT INTO nar_files (
    hash, compression, `query`, file_size, total_chunks
) VALUES (
    ?, ?, ?, ?, ?
)
ON DUPLICATE KEY UPDATE
    id = LAST_INSERT_ID(id),
    updated_at = CURRENT_TIMESTAMP;

-- name: LinkNarInfoToNarFile :exec
INSERT INTO narinfo_nar_files (
    narinfo_id, nar_file_id
) VALUES (
    ?, ?
)
ON DUPLICATE KEY UPDATE narinfo_id = narinfo_id;

-- name: LinkNarInfosByURLToNarFile :exec
INSERT IGNORE INTO narinfo_nar_files (narinfo_id, nar_file_id)
SELECT id, ?
FROM narinfos
WHERE url = ?;

-- name: TouchNarInfo :execrows
UPDATE narinfos
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: TouchNarFile :execrows
UPDATE nar_files
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = ? AND compression = ? AND `query` = ?;

-- name: DeleteNarInfoByHash :execrows
DELETE FROM narinfos
WHERE hash = ?;

-- name: DeleteNarFileByHash :execrows
DELETE FROM nar_files
WHERE hash = ? AND compression = ? AND `query` = ?;

-- name: DeleteNarInfoByID :execrows
DELETE FROM narinfos
WHERE id = ?;

-- name: DeleteNarFileByID :execrows
DELETE FROM nar_files
WHERE id = ?;

-- name: UpdateNarFileFileSize :exec
UPDATE nar_files
SET file_size = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteOrphanedNarFiles :execrows
DELETE FROM nar_files
WHERE id NOT IN (
    SELECT DISTINCT nar_file_id
    FROM narinfo_nar_files
);

-- name: DeleteOrphanedNarInfos :execrows
DELETE FROM narinfos
WHERE id NOT IN (
    SELECT DISTINCT narinfo_id
    FROM narinfo_nar_files
);

-- name: GetNarTotalSize :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS SIGNED) AS total_size
FROM nar_files;

-- name: GetNarInfoCount :one
SELECT CAST(COUNT(*) AS SIGNED) AS count
FROM narinfos;

-- name: GetNarFileCount :one
SELECT CAST(COUNT(*) AS SIGNED) AS count
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
) <= ?;

-- name: GetLeastUsedNarFiles :many
-- NOTE: This query uses a correlated subquery which is not optimal for performance.
-- The ideal implementation would use a window function (SUM OVER), but sqlc v1.30.0
-- does not properly support filtering on window function results in subqueries.
SELECT n1.id, n1.hash, n1.compression, n1.file_size, n1.`query`, n1.created_at, n1.updated_at, n1.last_accessed_at, n1.total_chunks, n1.chunking_started_at
FROM nar_files n1
WHERE (
    SELECT SUM(n2.file_size)
    FROM nar_files n2
    WHERE n2.last_accessed_at < n1.last_accessed_at
        OR (n2.last_accessed_at = n1.last_accessed_at AND n2.id <= n1.id)
) <= ?;

-- name: GetOrphanedNarFiles :many
-- Find files that have no relationship to any narinfo
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.`query`, nf.created_at, nf.updated_at, nf.last_accessed_at, nf.total_chunks, nf.chunking_started_at
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

-- name: IsNarInfoMigrated :one
-- Check if a narinfo hash has been migrated (has a URL).
SELECT EXISTS(
    SELECT 1
    FROM narinfos
    WHERE hash = ? AND url IS NOT NULL
) AS is_migrated;

-- name: GetMigratedNarInfoHashesPaginated :many
-- Get migrated narinfo hashes with pagination support.
SELECT hash
FROM narinfos
WHERE url IS NOT NULL
ORDER BY hash
LIMIT ? OFFSET ?;

-- name: GetChunkByHash :one
SELECT *
FROM chunks
WHERE hash = ?;

-- name: GetChunkByID :one
SELECT *
FROM chunks
WHERE id = ?;

-- name: GetChunksByNarFileID :many
SELECT c.id, c.hash, c.size, c.compressed_size, c.created_at, c.updated_at
FROM chunks c
INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.nar_file_id = ?
ORDER BY nfc.chunk_index;

-- name: CreateChunk :execresult
INSERT INTO chunks (
    hash, size, compressed_size
) VALUES (
    ?, ?, ?
)
ON DUPLICATE KEY UPDATE
    id = LAST_INSERT_ID(id),
    updated_at = CURRENT_TIMESTAMP;

-- name: LinkNarFileToChunk :exec
INSERT IGNORE INTO nar_file_chunks (
    nar_file_id, chunk_id, chunk_index
) VALUES (
    ?, ?, ?
);

-- @bulk-for LinkNarFileToChunk
-- name: LinkNarFileToChunks :exec
INSERT IGNORE INTO nar_file_chunks (
    nar_file_id, chunk_id, chunk_index
) VALUES (
    ?, ?, ?
);



-- name: GetTotalChunkSize :one
SELECT CAST(COALESCE(SUM(size), 0) AS SIGNED) AS total_size
FROM chunks;

-- name: GetChunkCount :one
SELECT CAST(COUNT(*) AS SIGNED) AS count
FROM chunks;

-- name: GetOrphanedChunks :many
SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
FROM chunks c
LEFT JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.chunk_id IS NULL;

-- name: DeleteChunkByID :exec
DELETE FROM chunks
WHERE id = ?;

-- name: DeleteNarFileChunksByNarFileID :exec
DELETE FROM nar_file_chunks
WHERE nar_file_id = ?;

-- name: GetChunkByNarFileIDAndIndex :one
SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
FROM chunks c
INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
WHERE nfc.nar_file_id = ? AND nfc.chunk_index = ?;

-- name: UpdateNarFileTotalChunks :exec
UPDATE nar_files
SET total_chunks = ?, file_size = ?, updated_at = CURRENT_TIMESTAMP, chunking_started_at = NULL
WHERE id = ?;

-- name: SetNarFileChunkingStarted :exec
UPDATE nar_files
SET chunking_started_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: GetNarInfoHashesToChunk :many
-- Get all narinfo hashes that have a URL (migrated) but whose NAR is not yet chunked.
SELECT ni.hash, ni.url
FROM narinfos ni
LEFT JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
LEFT JOIN nar_files nf ON nnf.nar_file_id = nf.id
WHERE ni.url IS NOT NULL
  AND (nf.id IS NULL OR nf.total_chunks = 0)
ORDER BY ni.hash;

-- name: GetNarFilesToChunk :many
-- Get all NAR files that are not yet chunked.
SELECT id, hash, compression, `query`, file_size
FROM nar_files
WHERE total_chunks = 0
ORDER BY id;

-- name: GetNarFilesToChunkCount :one
-- Get the count of NAR files that are not yet chunked.
SELECT COUNT(*)
FROM nar_files
WHERE total_chunks = 0;

-- name: GetCompressedNarInfos :many
SELECT id, hash, created_at, updated_at, last_accessed_at, store_path, url, compression, file_hash, file_size, nar_hash, nar_size, deriver, `system`, ca
FROM narinfos
WHERE compression NOT IN ('', 'none')
ORDER BY id
LIMIT ? OFFSET ?;

-- name: GetOldCompressedNarFiles :many
SELECT id, hash, compression, file_size, `query`, created_at, updated_at, last_accessed_at, total_chunks, chunking_started_at
FROM nar_files
WHERE compression NOT IN ('', 'none')
  AND created_at < ?
ORDER BY id
LIMIT ? OFFSET ?;

-- name: UpdateNarInfoCompressionAndURL :execrows
-- Update narinfo compression and URL after CDC migration.
UPDATE narinfos
SET compression = sqlc.arg(compression), url = sqlc.arg(new_url), updated_at = CURRENT_TIMESTAMP
WHERE url = sqlc.arg(old_url);
