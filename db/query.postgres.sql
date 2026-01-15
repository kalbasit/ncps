-- name: GetConfigByID :one
SELECT *
FROM config
WHERE id = $1;

-- name: GetConfigByKey :one
SELECT *
FROM config
WHERE key = $1;

-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = $1;

-- name: GetNarInfoByID :one
SELECT *
FROM narinfos
WHERE id = $1;

-- name: GetNarFileByHash :one
SELECT *
FROM nar_files
WHERE hash = $1;

-- name: GetNarFileByID :one
SELECT *
FROM nar_files
WHERE id = $1;

-- name: GetNarFileByNarInfoID :one
SELECT nf.*
FROM nar_files nf
INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
WHERE nnf.narinfo_id = $1;

-- name: GetNarInfoHashesByNarFileID :many
SELECT ni.hash
FROM narinfos ni
INNER JOIN narinfo_nar_files nnf ON ni.id = nnf.narinfo_id
WHERE nnf.nar_file_id = $1;

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
    hash
) VALUES (
    $1
)
RETURNING *;

-- name: CreateNarFile :one
INSERT INTO nar_files (
    hash, compression, query, file_size
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: LinkNarInfoToNarFile :exec
INSERT INTO narinfo_nar_files (
    narinfo_id, nar_file_id
) VALUES (
    $1, $2
);

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
WHERE hash = $1;

-- name: DeleteNarInfoByHash :execrows
DELETE FROM narinfos
WHERE hash = $1;

-- name: DeleteNarFileByHash :execrows
DELETE FROM nar_files
WHERE hash = $1;

-- name: DeleteNarInfoByID :execrows
DELETE FROM narinfos
WHERE id = $1;

-- name: DeleteNarFileByID :execrows
DELETE FROM nar_files
WHERE id = $1;

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
SELECT CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_size
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

-- name: GetLeastUsedNarFiles :many
-- NOTE: This query uses a correlated subquery which is not optimal for performance.
-- The ideal implementation would use a window function (SUM OVER), but sqlc v1.30.0
-- does not properly support filtering on window function results in subqueries.
SELECT n1.*
FROM nar_files n1
WHERE (
    SELECT SUM(n2.file_size)
    FROM nar_files n2
    WHERE n2.last_accessed_at < n1.last_accessed_at
       OR (n2.last_accessed_at = n1.last_accessed_at AND n2.id <= n1.id)
) <= $1;

-- name: GetOrphanedNarFiles :many
-- Find files that have no relationship to any narinfo
SELECT nf.*
FROM nar_files nf
LEFT JOIN narinfo_nar_files ninf ON nf.id = ninf.nar_file_id
WHERE ninf.narinfo_id IS NULL;

-- name: GetAllNarInfos :many
SELECT hash
FROM narinfos;
