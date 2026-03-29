package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"github.com/uptrace/bun/schema"
)

// CreateNarFileParams holds parameters for creating a NarFile.
type CreateNarFileParams struct {
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
	TotalChunks int64
}

// CreateNarFile inserts a new NarFile or upserts an existing one.
//
// UPSERT behavior by engine:
// - PostgreSQL/SQLite: ON CONFLICT (hash, compression, query) DO UPDATE SET updated_at = EXCLUDED.updated_at
// - MySQL: ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), updated_at = CURRENT_TIMESTAMP
//
// In both cases, the FileSize and other fields are NOT updated on conflict.
func CreateNarFile(ctx context.Context, db bun.IDB, arg CreateNarFileParams) (NarFile, error) {
	now := time.Now()

	switch db.Dialect().Name() {
	case dialect.MySQL:
		return createNarFileMySQL(ctx, db, arg, now)
	case dialect.PG, dialect.SQLite:
		// PostgreSQL and SQLite
		return createNarFilePostgresSQLite(ctx, db, arg, now)
	case dialect.Invalid, dialect.MSSQL, dialect.Oracle:
		fallthrough
	default:
		return createNarFilePostgresSQLite(ctx, db, arg, now)
	}
}

func createNarFilePostgresSQLite(
	ctx context.Context, db bun.IDB, arg CreateNarFileParams, _ time.Time,
) (NarFile, error) {
	// Note: We do NOT include created_at, updated_at, last_accessed_at in the INSERT.
	// This matches the old sqlc-generated code which relied on database defaults.
	// created_at defaults to CURRENT_TIMESTAMP
	// updated_at is NULL initially, and only updated via ON CONFLICT (to excluded.updated_at, which is NULL)
	// last_accessed_at defaults to CURRENT_TIMESTAMP
	query := `
		INSERT INTO nar_files (hash, compression, query, file_size, total_chunks)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (hash, compression, query) DO UPDATE SET
		    updated_at = EXCLUDED.updated_at
		RETURNING id, hash, compression, file_size, query, created_at, updated_at,
		    last_accessed_at, total_chunks, chunking_started_at, verified_at
	`

	var result NarFile

	err := db.NewRaw(query,
		arg.Hash, arg.Compression, arg.Query, arg.FileSize, arg.TotalChunks,
	).Scan(ctx, &result)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarFile{}, ErrNotFound
		}

		return NarFile{}, err
	}

	return result, nil
}

func createNarFileMySQL(ctx context.Context, db bun.IDB, arg CreateNarFileParams, _ time.Time) (NarFile, error) {
	// Note: We do NOT include created_at, updated_at, last_accessed_at in the INSERT.
	// This matches the old sqlc-generated code which relied on database defaults.
	query := `
		INSERT INTO nar_files (hash, compression, query, file_size, total_chunks)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		    id = LAST_INSERT_ID(id),
		    updated_at = CURRENT_TIMESTAMP
	`

	_, err := db.ExecContext(ctx, query,
		arg.Hash, arg.Compression, arg.Query, arg.FileSize, arg.TotalChunks,
	)
	if err != nil {
		return NarFile{}, err
	}

	// For MySQL, fetch after insert to get database-generated values
	return GetNarFileByHashAndCompressionAndQuery(ctx, db, arg.Hash, arg.Compression, arg.Query)
}

// GetNarFileByHashAndCompressionAndQuery retrieves a NarFile by its composite key.
func GetNarFileByHashAndCompressionAndQuery(
	ctx context.Context, db bun.IDB, hash, compression, query string,
) (NarFile, error) {
	var narFile NarFile

	err := db.NewSelect().Model(&narFile).
		Where("hash = ?", hash).
		Where("compression = ?", compression).
		Where("query = ?", query).
		Scan(ctx, &narFile)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarFile{}, ErrNotFound
		}

		return NarFile{}, err
	}

	return narFile, nil
}

// GetNarFileByID retrieves a NarFile by ID.
func GetNarFileByID(ctx context.Context, db bun.IDB, id int64) (NarFile, error) {
	var narFile NarFile

	err := db.NewSelect().Model(&narFile).Where("id = ?", id).Scan(ctx, &narFile)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarFile{}, ErrNotFound
		}

		return NarFile{}, err
	}

	return narFile, nil
}

// GetNarFileByNarInfoID retrieves a NarFile linked to a NarInfo.
func GetNarFileByNarInfoID(ctx context.Context, db bun.IDB, narinfoID int64) (NarFile, error) {
	var narFile NarFile

	err := db.NewRaw(`
		SELECT nf.* FROM nar_files nf
		INNER JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
		WHERE nnf.narinfo_id = ?
		LIMIT 1
	`, narinfoID).Scan(ctx, &narFile)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarFile{}, ErrNotFound
		}

		return NarFile{}, err
	}

	return narFile, nil
}

// GetNarFileCount returns the total count of NarFiles.
func GetNarFileCount(ctx context.Context, db bun.IDB) (int64, error) {
	count, err := db.NewSelect().Model(&NarFile{}).Count(ctx)

	return int64(count), err
}

// DeleteNarFileByHashParams holds parameters for deleting by hash.
type DeleteNarFileByHashParams struct {
	Hash        string
	Compression string
	Query       string
}

// DeleteNarFileByHash deletes a NarFile by its composite key.
func DeleteNarFileByHash(ctx context.Context, db bun.IDB, arg DeleteNarFileByHashParams) (int64, error) {
	result, err := db.NewDelete().Model(&NarFile{}).
		Where("hash = ?", arg.Hash).
		Where("compression = ?", arg.Compression).
		Where("query = ?", arg.Query).
		Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// DeleteNarFileByID deletes a NarFile by ID.
func DeleteNarFileByID(ctx context.Context, db bun.IDB, id int64) (int64, error) {
	result, err := db.NewDelete().Model(&NarFile{}).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// TouchNarFileParams holds parameters for touching a NarFile.
type TouchNarFileParams struct {
	Hash        string
	Compression string
	Query       string
}

// TouchNarFile updates the last_accessed_at timestamp.
func TouchNarFile(ctx context.Context, db bun.IDB, arg TouchNarFileParams) (int64, error) {
	now := time.Now()

	result, err := db.NewUpdate().Model(&NarFile{}).
		Set("last_accessed_at = ?", now).
		Set("updated_at = ?", now).
		Where("hash = ?", arg.Hash).
		Where("compression = ?", arg.Compression).
		Where("query = ?", arg.Query).
		Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// UpdateNarFileFileSizeParams holds parameters.
type UpdateNarFileFileSizeParams struct {
	FileSize uint64
	ID       int64
}

// UpdateNarFileFileSize updates the file size.
func UpdateNarFileFileSize(ctx context.Context, db bun.IDB, arg UpdateNarFileFileSizeParams) error {
	_, err := db.NewUpdate().Model(&NarFile{}).
		Set("file_size = ?", arg.FileSize).
		Where("id = ?", arg.ID).
		Exec(ctx)

	return err
}

// UpdateNarFileTotalChunksParams holds parameters.
type UpdateNarFileTotalChunksParams struct {
	TotalChunks int64
	FileSize    uint64
	ID          int64
}

// UpdateNarFileTotalChunks updates the total chunks count and file size.
func UpdateNarFileTotalChunks(ctx context.Context, db bun.IDB, arg UpdateNarFileTotalChunksParams) error {
	_, err := db.NewUpdate().Model(&NarFile{}).
		Set("total_chunks = ?", arg.TotalChunks).
		Set("file_size = ?", arg.FileSize).
		Where("id = ?", arg.ID).
		Exec(ctx)

	return err
}

// SetNarFileChunkingStarted sets the chunking_started_at timestamp.
func SetNarFileChunkingStarted(ctx context.Context, db bun.IDB, id int64) error {
	_, err := db.NewUpdate().Model(&NarFile{}).
		Set("chunking_started_at = ?", time.Now()).
		Where("id = ?", id).
		Exec(ctx)

	return err
}

// ClearNarFileChunkingStarted clears the chunking_started_at timestamp.
func ClearNarFileChunkingStarted(ctx context.Context, db bun.IDB, id int64) error {
	_, err := db.NewUpdate().Model(&NarFile{}).
		Set("chunking_started_at = NULL").
		Where("id = ?", id).
		Exec(ctx)

	return err
}

// UpdateNarFileVerifiedAt updates the verified_at timestamp.
func UpdateNarFileVerifiedAt(ctx context.Context, db bun.IDB, id int64) error {
	_, err := db.NewUpdate().Model(&NarFile{}).
		Set("verified_at = ?", time.Now()).
		Where("id = ?", id).
		Exec(ctx)

	return err
}

// GetNarInfoURLByNarFileHashParams holds parameters.
type GetNarInfoURLByNarFileHashParams struct {
	Hash        string
	Compression string
	Query       string
}

// GetNarInfoURLByNarFileHash returns the URL from a NarInfo linked to a NarFile.
func GetNarInfoURLByNarFileHash(
	ctx context.Context, db bun.IDB, arg GetNarInfoURLByNarFileHashParams,
) (sql.NullString, error) {
	var url sql.NullString

	err := db.NewRaw(`
		SELECT n.url FROM narinfos n
		INNER JOIN narinfo_nar_files nnf ON n.id = nnf.narinfo_id
		INNER JOIN nar_files nf ON nf.id = nnf.nar_file_id
		WHERE nf.hash = ? AND nf.compression = ? AND nf.query = ?
		LIMIT 1
	`, arg.Hash, arg.Compression, arg.Query).
		Scan(ctx, &url)
	if err != nil {
		return sql.NullString{}, err
	}

	return url, nil
}

// GetOrphanedNarFiles returns NarFiles not linked to any NarInfo.
func GetOrphanedNarFiles(ctx context.Context, db bun.IDB) ([]NarFile, error) {
	var narFiles []NarFile

	err := db.NewRaw(`
		SELECT nf.* FROM nar_files nf
		LEFT JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
		WHERE nnf.nar_file_id IS NULL
	`).Scan(ctx, &narFiles)

	return narFiles, err
}

// GetOrphanedNarFilesCount returns the count of orphaned NarFiles.
func GetOrphanedNarFilesCount(ctx context.Context, db bun.IDB) (int64, error) {
	var count int64

	err := db.NewRaw(`
		SELECT COUNT(*) FROM nar_files nf
		LEFT JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
		WHERE nnf.nar_file_id IS NULL
	`).Scan(ctx, &count)

	return count, err
}

// DeleteOrphanedNarFiles deletes NarFiles not linked to any NarInfo.
func DeleteOrphanedNarFiles(ctx context.Context, db bun.IDB) (int64, error) {
	result, err := db.NewRaw(`
		DELETE FROM nar_files WHERE id IN (
			SELECT nf.id FROM nar_files nf
			LEFT JOIN narinfo_nar_files nnf ON nf.id = nnf.nar_file_id
			WHERE nnf.nar_file_id IS NULL
		)
	`).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// GetOldCompressedNarFilesRow holds result of GetOldCompressedNarFiles.
type GetOldCompressedNarFilesRow struct {
	ID          int64
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
	CreatedAt   time.Time
}

// GetOldCompressedNarFiles returns old NarFiles with non-empty compression.
func GetOldCompressedNarFiles(
	ctx context.Context, db bun.IDB, cutoffTime time.Time,
) ([]GetOldCompressedNarFilesRow, error) {
	var rows []GetOldCompressedNarFilesRow

	err := db.NewSelect().Model(&NarFile{}).
		Column("id", "hash", "compression", "query", "file_size", "created_at").
		Where("compression != ''").
		Where("created_at < ?", cutoffTime).
		Scan(ctx, &rows)

	return rows, err
}

// GetStuckNarFilesParams holds parameters for getting stuck files.
type GetStuckNarFilesParams struct {
	CutoffTime time.Time
	BatchSize  int32
}

// GetStuckNarFilesRow holds result of GetStuckNarFiles.
type GetStuckNarFilesRow struct {
	ID          int64
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
}

// GetStuckNarFiles returns NarFiles with chunking_started_at set but not completed.
func GetStuckNarFiles(ctx context.Context, db bun.IDB, arg GetStuckNarFilesParams) ([]GetStuckNarFilesRow, error) {
	var rows []GetStuckNarFilesRow

	err := db.NewSelect().Model(&NarFile{}).
		Column("id", "hash", "compression", "query", "file_size").
		Where("chunking_started_at IS NOT NULL").
		Where("chunking_started_at < ?", arg.CutoffTime).
		Where("total_chunks = 0").
		Limit(int(arg.BatchSize)).
		Scan(ctx, &rows)

	return rows, err
}

// GetNarFilesToChunkRow holds result of GetNarFilesToChunk.
type GetNarFilesToChunkRow struct {
	ID          int64
	Hash        string
	Compression string
	Query       string
	FileSize    uint64
}

// GetNarFilesToChunk returns NarFiles eligible for chunking.
func GetNarFilesToChunk(ctx context.Context, db bun.IDB) ([]GetNarFilesToChunkRow, error) {
	var rows []GetNarFilesToChunkRow

	err := db.NewSelect().Model(&NarFile{}).
		Column("id", "hash", "compression", "query", "file_size").
		Where("total_chunks = 0").
		Where("chunking_started_at IS NULL").
		Scan(ctx, &rows)

	return rows, err
}

// GetNarFilesToChunkCount returns count of NarFiles eligible for chunking.
func GetNarFilesToChunkCount(ctx context.Context, db bun.IDB) (int64, error) {
	count, err := db.NewSelect().Model(&NarFile{}).
		Where("total_chunks = 0").
		Where("chunking_started_at IS NULL").
		Count(ctx)

	return int64(count), err
}

// GetAllNarFilesRow holds result of GetAllNarFiles.
type GetAllNarFilesRow struct {
	ID                int64
	Hash              string
	Compression       string
	Query             string
	FileSize          uint64
	TotalChunks       int64
	ChunkingStartedAt schema.NullTime
	CreatedAt         time.Time
	UpdatedAt         schema.NullTime
	LastAccessedAt    schema.NullTime
	VerifiedAt        schema.NullTime
}

// GetAllNarFiles returns all NarFiles.
func GetAllNarFiles(ctx context.Context, db bun.IDB) ([]GetAllNarFilesRow, error) {
	var rows []GetAllNarFilesRow

	err := db.NewSelect().Model(&NarFile{}).Scan(ctx, &rows)

	return rows, err
}

// HasAnyChunkedNarFiles returns true if any NarFile has chunks.
func HasAnyChunkedNarFiles(ctx context.Context, db bun.IDB) (bool, error) {
	count, err := db.NewSelect().Model(&NarFile{}).Where("total_chunks > 0").Count(ctx)

	return count > 0, err
}
