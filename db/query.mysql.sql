-- name: GetConfigByKey :one
SELECT *
FROM config
WHERE `key` = ?;

-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = ?;

-- name: GetNarInfoHashByNarURL :one
SELECT hash
FROM narinfos
WHERE url = ?
LIMIT 1;

-- name: GetNarFileByHashAndCompressionAndQuery :one
SELECT id, hash, compression, file_size, `query`, created_at, updated_at, last_accessed_at, total_chunks, chunking_started_at
FROM nar_files
WHERE hash = ? AND compression = ? AND `query` = ?;

-- name: GetNarFileByNarInfoID :one
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.`query`, nf.created_at, nf.updated_at, nf.last_accessed_at, nf.total_chunks, nf.chunking_started_at
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
WHERE nnf.narinfo_id = ?;

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

-- name: GetChunkByHash :one
SELECT *
FROM chunks
WHERE hash = ?;

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

-- name: GetChunkCount :one
SELECT CAST(COUNT(*) AS SIGNED) AS count
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
SELECT id, hash, compression, `query`, file_size, total_chunks, chunking_started_at, created_at, updated_at, last_accessed_at
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
SELECT EXISTS(SELECT 1 FROM nar_files WHERE total_chunks > 0);
