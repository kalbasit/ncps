-- name: GetNarInfoByHash :one
SELECT *
FROM narinfos
WHERE hash = ?;

-- name: GetNarInfoByID :one
SELECT *
FROM narinfos
WHERE id = ?;

-- name: GetNarByHash :one
SELECT *
FROM nars
WHERE hash = ?;

-- name: GetNarByID :one
SELECT *
FROM nars
WHERE id = ?;

-- name: CreateNarInfo :one
INSERT into narinfos (
  hash
) VALUES (
  ?
)
RETURNING *;

-- name: CreateNar :one
INSERT into nars (
  narinfo_id, hash, compression, file_size
) VALUES (
  ?, ?, ?, ?
)
RETURNING *;

-- name: TouchNarInfo :exec
UPDATE narinfos
SET last_accessed_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: TouchNar :exec
UPDATE nars
SET last_accessed_at = CURRENT_TIMESTAMP,
  updated_at = CURRENT_TIMESTAMP
WHERE hash = ?;

-- name: DeleteNarInfoByHash :exec
DELETE FROM narinfos
WHERE hash = ?;

-- name: DeleteNarByHash :exec
DELETE FROM nars
WHERE hash = ?;

-- name: DeleteNarInfoByID :exec
DELETE FROM narinfos
WHERE id = ?;

-- name: DeleteNarByID :exec
DELETE FROM nars
WHERE id = ?;

-- name: GetNarTotalSize :one
SELECT SUM(file_size) AS total_size
FROM nars;

-- name: GetLeastUsedNars :many
SELECT
  n1.*
FROM nars n1
WHERE (
  SELECT SUM(n2.file_size)
  FROM nars n2
  WHERE n2.last_accessed_at <= n1.last_accessed_at
) <= ?;
