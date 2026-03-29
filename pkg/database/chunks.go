package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// CreateChunkParams holds parameters for creating a Chunk.
type CreateChunkParams struct {
	Hash           string
	Size           uint32
	CompressedSize uint32
}

// CreateChunk inserts a new Chunk or upserts an existing one.
func CreateChunk(ctx context.Context, db bun.IDB, arg CreateChunkParams) (Chunk, error) {
	chunk := &Chunk{
		Hash:           arg.Hash,
		Size:           arg.Size,
		CompressedSize: arg.CompressedSize,
		CreatedAt:      time.Now(),
	}

	_, err := db.NewInsert().Model(chunk).Ignore().Exec(ctx)
	if err != nil {
		return Chunk{}, err
	}
	// Fetch to get database-generated timestamps (also handles upsert on conflict)
	return GetChunkByHash(ctx, db, arg.Hash)
}

// GetChunkByHash retrieves a Chunk by hash.
func GetChunkByHash(ctx context.Context, db bun.IDB, hash string) (Chunk, error) {
	var chunk Chunk

	err := db.NewSelect().Model(&chunk).Where("hash = ?", hash).Scan(ctx, &chunk)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Chunk{}, ErrNotFound
		}

		return Chunk{}, err
	}

	return chunk, nil
}

// GetChunkByID retrieves a Chunk by ID.
func GetChunkByID(ctx context.Context, db bun.IDB, id int64) (Chunk, error) {
	var chunk Chunk

	err := db.NewSelect().Model(&chunk).Where("id = ?", id).Scan(ctx, &chunk)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Chunk{}, ErrNotFound
		}

		return Chunk{}, err
	}

	return chunk, nil
}

// DeleteChunkByID deletes a Chunk by ID.
func DeleteChunkByID(ctx context.Context, db bun.IDB, id int64) error {
	_, err := db.NewDelete().Model(&Chunk{}).Where("id = ?", id).Exec(ctx)

	return err
}

// GetAllChunks returns all Chunks.
func GetAllChunks(ctx context.Context, db bun.IDB) ([]Chunk, error) {
	var chunks []Chunk

	err := db.NewSelect().Model(&chunks).Scan(ctx, &chunks)

	return chunks, err
}

// GetChunkCount returns the total count of Chunks.
func GetChunkCount(ctx context.Context, db bun.IDB) (int64, error) {
	count, err := db.NewSelect().Model(&Chunk{}).Count(ctx)

	return int64(count), err
}

// GetChunkByNarFileIDAndIndexParams holds parameters.
type GetChunkByNarFileIDAndIndexParams struct {
	NarFileID  int64
	ChunkIndex int64
}

// GetChunkByNarFileIDAndIndexRow holds the result.
type GetChunkByNarFileIDAndIndexRow struct {
	ID        int64
	Hash      string
	Size      uint32
	CreatedAt time.Time
	UpdatedAt schema.NullTime
}

// GetChunkByNarFileIDAndIndex retrieves a Chunk by NarFile and index.
func GetChunkByNarFileIDAndIndex(
	ctx context.Context, db bun.IDB, arg GetChunkByNarFileIDAndIndexParams,
) (GetChunkByNarFileIDAndIndexRow, error) {
	var row GetChunkByNarFileIDAndIndexRow

	err := db.NewRaw(`
		SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
		FROM chunks c
		INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
		WHERE nfc.nar_file_id = ? AND nfc.chunk_index = ?
	`, arg.NarFileID, arg.ChunkIndex).Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GetChunkByNarFileIDAndIndexRow{}, ErrNotFound
		}

		return GetChunkByNarFileIDAndIndexRow{}, err
	}

	return row, nil
}

// GetChunksByNarFileID retrieves all Chunks for a NarFile.
func GetChunksByNarFileID(ctx context.Context, db bun.IDB, narFileID int64) ([]Chunk, error) {
	var chunks []Chunk

	err := db.NewRaw(`
		SELECT c.* FROM chunks c
		INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
		WHERE nfc.nar_file_id = ?
		ORDER BY nfc.chunk_index
	`, narFileID).Scan(ctx, &chunks)

	return chunks, err
}

// GetChunksByNarFileIDFromIndexParams holds parameters.
type GetChunksByNarFileIDFromIndexParams struct {
	NarFileID  int64
	ChunkIndex int64
	Limit      int32
}

// GetChunksByNarFileIDFromIndexRow holds the result.
type GetChunksByNarFileIDFromIndexRow struct {
	ID        int64
	Hash      string
	Size      uint32
	CreatedAt time.Time
	UpdatedAt schema.NullTime
}

// GetChunksByNarFileIDFromIndex retrieves Chunks starting from an index.
func GetChunksByNarFileIDFromIndex(
	ctx context.Context, db bun.IDB, arg GetChunksByNarFileIDFromIndexParams,
) ([]GetChunksByNarFileIDFromIndexRow, error) {
	var rows []GetChunksByNarFileIDFromIndexRow

	err := db.NewRaw(`
		SELECT c.id, c.hash, c.size, c.created_at, c.updated_at
		FROM chunks c
		INNER JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
		WHERE nfc.nar_file_id = ? AND nfc.chunk_index >= ?
		ORDER BY nfc.chunk_index
		LIMIT ?
	`, arg.NarFileID, arg.ChunkIndex, arg.Limit).Scan(ctx, &rows)

	return rows, err
}

// LinkNarFileToChunkParams holds parameters.
type LinkNarFileToChunkParams struct {
	NarFileID  int64
	ChunkID    int64
	ChunkIndex int64
}

// LinkNarFileToChunk creates a link between a NarFile and a Chunk.
func LinkNarFileToChunk(ctx context.Context, db bun.IDB, arg LinkNarFileToChunkParams) error {
	link := &NarFileChunk{
		NarFileID:  arg.NarFileID,
		ChunkID:    arg.ChunkID,
		ChunkIndex: arg.ChunkIndex,
	}
	_, err := db.NewInsert().Model(link).Ignore().Exec(ctx)

	return err
}

// LinkNarFileToChunksParams holds parameters for bulk linking.
type LinkNarFileToChunksParams struct {
	NarFileID  int64
	ChunkID    []int64
	ChunkIndex []int64
}

// LinkNarFileToChunks creates links between a NarFile and multiple Chunks.
func LinkNarFileToChunks(ctx context.Context, db bun.IDB, arg LinkNarFileToChunksParams) error {
	if len(arg.ChunkIndex) != len(arg.ChunkID) {
		return ErrMismatchedSlices
	}

	for i, chunkID := range arg.ChunkID {
		link := &NarFileChunk{
			NarFileID:  arg.NarFileID,
			ChunkID:    chunkID,
			ChunkIndex: arg.ChunkIndex[i],
		}

		_, err := db.NewInsert().Model(link).Ignore().Exec(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// DeleteNarFileChunksByNarFileID deletes all Chunk links for a NarFile.
func DeleteNarFileChunksByNarFileID(ctx context.Context, db bun.IDB, narFileID int64) error {
	_, err := db.NewDelete().Model(&NarFileChunk{}).Where("nar_file_id = ?", narFileID).Exec(ctx)

	return err
}

// GetOrphanedChunksRow holds the result of GetOrphanedChunks.
type GetOrphanedChunksRow struct {
	ID             int64
	Hash           string
	Size           uint32
	CompressedSize uint32
	CreatedAt      time.Time
	UpdatedAt      schema.NullTime
}

// GetOrphanedChunks returns Chunks not linked to any NarFile.
func GetOrphanedChunks(ctx context.Context, db bun.IDB) ([]GetOrphanedChunksRow, error) {
	var rows []GetOrphanedChunksRow

	err := db.NewRaw(`
		SELECT c.* FROM chunks c
		LEFT JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
		WHERE nfc.chunk_id IS NULL
	`).Scan(ctx, &rows)

	return rows, err
}

// DeleteOrphanedChunks deletes Chunks not linked to any NarFile.
func DeleteOrphanedChunks(ctx context.Context, db bun.IDB) (int64, error) {
	result, err := db.NewRaw(`
		DELETE FROM chunks WHERE id IN (
			SELECT c.id FROM chunks c
			LEFT JOIN nar_file_chunks nfc ON c.id = nfc.chunk_id
			WHERE nfc.chunk_id IS NULL
		)
	`).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}
