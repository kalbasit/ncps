-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = $1;

-- name: GetNarInfoByID :one
SELECT *
FROM narinfos
WHERE id = $1;

-- name: GetNarByHash :one
SELECT *
FROM nars
WHERE hash = $1;

-- name: GetNarByID :one
SELECT *
FROM nars
WHERE id = $1;

-- name: CreateNarInfo :one
INSERT INTO narinfos (
    hash
) VALUES (
    $1
)
RETURNING *;

-- name: CreateNar :one
INSERT INTO nars (
    narinfo_id, hash, compression, query, file_size
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: TouchNarInfo :execrows
UPDATE narinfos
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = $1;

-- name: TouchNar :execrows
UPDATE nars
SET
    last_accessed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE hash = $1;

-- name: DeleteNarInfoByHash :execrows
DELETE FROM narinfos
WHERE hash = $1;

-- name: DeleteNarByHash :execrows
DELETE FROM nars
WHERE hash = $1;

-- name: DeleteNarInfoByID :execrows
DELETE FROM narinfos
WHERE id = $1;

-- name: DeleteNarByID :execrows
DELETE FROM nars
WHERE id = $1;

-- name: GetNarTotalSize :one
SELECT CAST(COALESCE(SUM(file_size), 0) AS BIGINT) AS total_size
FROM nars;

-- name: GetLeastUsedNars :many
SELECT n1.*
FROM nars n1
WHERE (
    SELECT SUM(n2.file_size)
    FROM nars n2
    WHERE n2.last_accessed_at <= n1.last_accessed_at
) <= $1;
