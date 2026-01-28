-- name: GetConfigByID :one
SELECT *
FROM config
WHERE id = ?;

-- name: GetConfigByKey :one
SELECT *
FROM config
WHERE key = ?;

-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = ?;

-- name: GetNarInfoByID :one
SELECT *
FROM narinfos
WHERE id = ?;


-- name: GetNarFileByHashAndCompressionAndQuery :one
SELECT id, hash, compression, file_size, "query", created_at, updated_at, last_accessed_at
FROM nar_files
WHERE hash = ? AND compression = ? AND "query" = ?;

-- name: GetNarFileByID :one
SELECT id, hash, compression, file_size, "query", created_at, updated_at, last_accessed_at
FROM nar_files
WHERE id = ?;

-- name: GetNarFileByNarInfoID :one
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf.query, nf.created_at, nf.updated_at, nf.last_accessed_at
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
WHERE nnf.narinfo_id = ?;

-- name: GetNarInfoHashesByNarFileID :many
SELECT ni.hash
FROM narinfos ni
INNER JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
WHERE nnf.nar_file_id = ?;

-- name: CreateConfig :one
INSERT INTO config (
    key, value
) VALUES (
    ?, ?
)
RETURNING *;

-- name: SetConfig :exec
INSERT INTO config (
    key, value
) VALUES (
    ?, ?
)
ON CONFLICT(key)
DO UPDATE SET
  value = EXCLUDED.value,
  updated_at = CURRENT_TIMESTAMP;

-- name: CreateNarInfo :one
INSERT INTO narinfos (
    hash, store_path, url, compression, file_hash, file_size, nar_hash, nar_size, deriver, system, ca
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT(hash) DO UPDATE SET
    store_path = excluded.store_path,
    url = excluded.url,
    compression = excluded.compression,
    file_hash = excluded.file_hash,
    file_size = excluded.file_size,
    nar_hash = excluded.nar_hash,
    nar_size = excluded.nar_size,
    deriver = excluded.deriver,
    system = excluded.system,
    ca = excluded.ca,
    updated_at = CURRENT_TIMESTAMP
WHERE narinfos.url IS NULL
RETURNING *;

-- name: AddNarInfoReference :exec
INSERT INTO narinfo_references (
    narinfo_id, reference
) VALUES (
    ?, ?
);

-- name: AddNarInfoSignature :exec
INSERT INTO narinfo_signatures (
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

-- name: CreateNarFile :one
INSERT INTO nar_files (
    hash, compression, "query", file_size
) VALUES (
    ?, ?, ?, ?
)
ON CONFLICT (hash, compression, "query") DO UPDATE SET
    updated_at = excluded.updated_at
RETURNING id, hash, compression, file_size, "query", created_at, updated_at, last_accessed_at;

-- name: LinkNarInfoToNarFile :exec
INSERT INTO narinfo_nar_files (
    narinfo_id, nar_file_id
) VALUES (
    ?, ?
)
ON CONFLICT (narinfo_id, nar_file_id) DO NOTHING;

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
WHERE hash = ? AND compression = ? AND "query" = ?;

-- name: DeleteNarInfoByHash :execrows
DELETE FROM narinfos
WHERE hash = ?;

-- name: DeleteNarFileByHash :execrows
DELETE FROM nar_files
WHERE hash = ? AND compression = ? AND "query" = ?;

-- name: DeleteNarInfoByID :execrows
DELETE FROM narinfos
WHERE id = ?;

-- name: DeleteNarFileByID :execrows
DELETE FROM nar_files
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
SELECT CAST(COALESCE(SUM(file_size), 0) AS INTEGER) AS total_size
FROM nar_files;

-- name: GetNarInfoCount :one
SELECT CAST(COUNT(*) AS INTEGER) AS count
FROM narinfos;

-- name: GetNarFileCount :one
SELECT CAST(COUNT(*) AS INTEGER) AS count
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
SELECT n1.id, n1.hash, n1.compression, n1.file_size, n1."query", n1.created_at, n1.updated_at, n1.last_accessed_at
FROM nar_files n1
WHERE (
    SELECT SUM(n2.file_size)
    FROM nar_files n2
    WHERE n2.last_accessed_at < n1.last_accessed_at
        OR (n2.last_accessed_at = n1.last_accessed_at AND n2.id <= n1.id)
) <= ?;

-- name: GetOrphanedNarFiles :many
-- Find files that have no relationship to any narinfo
SELECT nf.id, nf.hash, nf.compression, nf.file_size, nf."query", nf.created_at, nf.updated_at, nf.last_accessed_at
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
