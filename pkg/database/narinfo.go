package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

// CreateNarInfoParams holds parameters for creating a NarInfo.
type CreateNarInfoParams struct {
	Hash        string
	StorePath   sql.NullString
	URL         sql.NullString
	Compression sql.NullString
	FileHash    sql.NullString
	FileSize    sql.NullInt64
	NarHash     sql.NullString
	NarSize     sql.NullInt64
	Deriver     sql.NullString
	System      sql.NullString
	Ca          sql.NullString
}

// CreateNarInfo inserts a new NarInfo or updates an existing one.
//
// UPSERT behavior by engine:
// - PostgreSQL/SQLite: ON CONFLICT (hash) DO UPDATE SET ... WHERE url IS NULL
//   - If URL is NULL: updates the record and returns it via RETURNING
//   - If URL is NOT NULL: no update, no rows returned -> ErrNotFound
//
// - MySQL: ON DUPLICATE KEY UPDATE col = IF(url IS NULL, VALUES(col), col)
//   - Same semantics using IF() instead of WHERE
func CreateNarInfo(ctx context.Context, db bun.IDB, arg CreateNarInfoParams) (NarInfo, error) {
	now := time.Now()

	switch db.Dialect().Name() {
	case dialect.MySQL:
		return createNarInfoMySQL(ctx, db, arg, now)
	case dialect.PG, dialect.SQLite:
		// PostgreSQL and SQLite use ON CONFLICT ... WHERE url IS NULL
		return createNarInfoPostgresSQLite(ctx, db, arg, now)
	case dialect.Invalid, dialect.MSSQL, dialect.Oracle:
		fallthrough
	default:
		return createNarInfoPostgresSQLite(ctx, db, arg, now)
	}
}

func createNarInfoPostgresSQLite(
	ctx context.Context, db bun.IDB, arg CreateNarInfoParams, now time.Time,
) (NarInfo, error) {
	// When url IS NULL (record from PutNar), we update URL-related fields.
	// Do NOT update file_size here because CheckAndFixNarInfo may have already
	// corrected it to match the actual NAR content. Updating it would overwrite
	// the corrected value with the wrong value from PutNarInfo.
	// We DO update file_hash because it's provided by upstream and is correct.
	//
	// Note: updated_at is NOT set on INSERT - it remains NULL. The TouchNarInfo
	// function sets updated_at when the record is touched.
	query := `
		INSERT INTO narinfos (hash, store_path, url, compression, file_hash,
		    file_size, nar_hash, nar_size, deriver, system, ca, created_at,
		    last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (hash) DO UPDATE SET
		    store_path = excluded.store_path,
		    url = excluded.url,
		    compression = excluded.compression,
		    file_hash = excluded.file_hash,
		    nar_hash = excluded.nar_hash,
		    nar_size = excluded.nar_size,
		    deriver = excluded.deriver,
		    system = excluded.system,
		    ca = excluded.ca,
		    updated_at = CURRENT_TIMESTAMP
		WHERE narinfos.url IS NULL
		RETURNING id, hash, created_at, updated_at, last_accessed_at, store_path,
		    url, compression, file_hash, file_size, nar_hash, nar_size,
		    deriver, system, ca
	`

	var result NarInfo

	err := db.NewRaw(query,
		arg.Hash, arg.StorePath, arg.URL, arg.Compression, arg.FileHash,
		arg.FileSize, arg.NarHash, arg.NarSize, arg.Deriver, arg.System, arg.Ca,
		now, now,
	).Scan(ctx, &result)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarInfo{}, ErrNotFound
		}

		return NarInfo{}, err
	}

	return result, nil
}

func createNarInfoMySQL(ctx context.Context, db bun.IDB, arg CreateNarInfoParams, now time.Time) (NarInfo, error) {
	// MySQL implementation: try INSERT first. If we get a duplicate key error,
	// it means a placeholder exists (url=NULL) and we should UPDATE it.
	//
	// We don't use ON DUPLICATE KEY UPDATE because the CASE/WHEN logic for
	// detecting NULL and applying updates has proven unreliable in MySQL.
	insertQuery := `
		INSERT INTO narinfos (hash, store_path, url, compression, file_hash,
		    file_size, nar_hash, nar_size, deriver, system, ca, created_at,
		    updated_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.ExecContext(ctx, insertQuery,
		arg.Hash, arg.StorePath, arg.URL, arg.Compression, arg.FileHash,
		arg.FileSize, arg.NarHash, arg.NarSize, arg.Deriver, arg.System, arg.Ca,
		now, nil, now,
	)
	if err != nil {
		// Check if it's a duplicate key error (MySQL error 1062)
		if !isDuplicateKeyError(err) {
			return NarInfo{}, err
		}
		// Duplicate key - fall through to UPDATE below
	} else {
		// INSERT succeeded - return the created record
		narInfo, err := GetNarInfoByHash(ctx, db, arg.Hash)
		if err != nil {
			return NarInfo{}, err
		}

		return narInfo, nil
	}

	// Duplicate key case: placeholder exists, UPDATE it
	updateQuery := `
		UPDATE narinfos SET
		    store_path = ?,
		    url = ?,
		    compression = ?,
		    file_hash = ?,
		    nar_hash = ?,
		    nar_size = ?,
		    deriver = ?,
		    system = ?,
		    ca = ?,
		    updated_at = CURRENT_TIMESTAMP,
		    last_accessed_at = ?
		WHERE hash = ? AND url IS NULL
	`

	_, err = db.ExecContext(ctx, updateQuery,
		arg.StorePath, arg.URL, arg.Compression, arg.FileHash,
		arg.NarHash, arg.NarSize, arg.Deriver, arg.System, arg.Ca,
		now, arg.Hash,
	)
	if err != nil {
		return NarInfo{}, err
	}

	// Fetch and return the updated record
	return GetNarInfoByHash(ctx, db, arg.Hash)
}

// isDuplicateKeyError checks if the error is a MySQL duplicate key error.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// MySQL error 1062: Duplicate entry
	return strings.Contains(err.Error(), "Duplicate entry") ||
		strings.Contains(err.Error(), "1062")
}

// GetNarInfoByHash retrieves a NarInfo by its hash.
func GetNarInfoByHash(ctx context.Context, db bun.IDB, hash string) (NarInfo, error) {
	var narInfo NarInfo

	err := db.NewSelect().Model(&narInfo).Where("hash = ?", hash).Scan(ctx, &narInfo)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarInfo{}, ErrNotFound
		}

		return NarInfo{}, err
	}

	return narInfo, nil
}

// GetNarInfoByID retrieves a NarInfo by its ID.
func GetNarInfoByID(ctx context.Context, db bun.IDB, id int64) (NarInfo, error) {
	var narInfo NarInfo

	err := db.NewSelect().Model(&narInfo).Where("id = ?", id).Scan(ctx, &narInfo)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NarInfo{}, ErrNotFound
		}

		return NarInfo{}, err
	}

	return narInfo, nil
}

// UpdateNarInfoParams holds parameters for updating a NarInfo.
type UpdateNarInfoParams struct {
	Hash        string
	StorePath   sql.NullString
	URL         sql.NullString
	Compression sql.NullString
	FileHash    sql.NullString
	FileSize    sql.NullInt64
	NarHash     sql.NullString
	NarSize     sql.NullInt64
	Deriver     sql.NullString
	System      sql.NullString
	Ca          sql.NullString
}

// UpdateNarInfo updates a NarInfo by hash.
func UpdateNarInfo(ctx context.Context, db bun.IDB, arg UpdateNarInfoParams) (NarInfo, error) {
	narInfo := &NarInfo{
		Hash:        arg.Hash,
		StorePath:   arg.StorePath,
		URL:         arg.URL,
		Compression: arg.Compression,
		FileHash:    arg.FileHash,
		FileSize:    arg.FileSize,
		NarHash:     arg.NarHash,
		NarSize:     arg.NarSize,
		Deriver:     arg.Deriver,
		System:      arg.System,
		Ca:          arg.Ca,
	}

	// Use .Column() to only update specific fields, avoiding CreatedAt which
	// would be set to zero if we let bun update all fields
	res, err := db.NewUpdate().Model(narInfo).
		Column("store_path", "url", "compression", "file_hash", "file_size",
			"nar_hash", "nar_size", "deriver", "system", "ca").
		Where("hash = ?", arg.Hash).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return NarInfo{}, err
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return NarInfo{}, err
	}

	if rowsAffected == 0 {
		return NarInfo{}, ErrNotFound
	}

	return *narInfo, nil
}

// DeleteNarInfoByHash deletes a NarInfo by hash.
func DeleteNarInfoByHash(ctx context.Context, db bun.IDB, hash string) (int64, error) {
	result, err := db.NewDelete().Model(&NarInfo{}).Where("hash = ?", hash).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// DeleteNarInfoByID deletes a NarInfo by ID.
func DeleteNarInfoByID(ctx context.Context, db bun.IDB, id int64) (int64, error) {
	result, err := db.NewDelete().Model(&NarInfo{}).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// TouchNarInfo updates the last_accessed_at timestamp.
func TouchNarInfo(ctx context.Context, db bun.IDB, hash string) (int64, error) {
	now := time.Now()

	result, err := db.NewUpdate().Model(&NarInfo{}).
		Set("last_accessed_at = ?", now).
		Set("updated_at = ?", now).
		Where("hash = ?", hash).
		Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// GetNarInfoCount returns the total count of NarInfos.
func GetNarInfoCount(ctx context.Context, db bun.IDB) (int64, error) {
	count, err := db.NewSelect().Model(&NarInfo{}).Count(ctx)

	return int64(count), err
}

// GetLeastUsedNarInfos returns NarInfos ordered by last_accessed_at where the
// cumulative sum of file sizes (from oldest to newest) is less than or equal to
// the specified fileSize threshold. This implements LRU eviction correctly by
// selecting the oldest entries whose total size meets the cleanup threshold.
//
// The query uses a correlated subquery to calculate the running sum of file sizes
// for all NarInfos with last_accessed_at less than or equal to the current entry.
func GetLeastUsedNarInfos(ctx context.Context, db bun.IDB, fileSize uint64) ([]NarInfo, error) {
	var narInfos []NarInfo

	var query string
	if db.Dialect().Name() == dialect.MySQL {
		// MySQL doesn't support NULLS FIRST, use ISNULL() to sort NULLs first
		query = `
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
			) <= ?
			ORDER BY ISNULL(ni1.last_accessed_at), ni1.last_accessed_at ASC
		`
	} else {
		// PostgreSQL and SQLite support NULLS FIRST
		query = `
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
			) <= ?
			ORDER BY ni1.last_accessed_at ASC NULLS FIRST
		`
	}

	err := db.NewRaw(query, fileSize).Scan(ctx, &narInfos)

	return narInfos, err
}

// GetMigratedNarInfoHashes returns hashes of NarInfos that have been migrated (have URL set).
func GetMigratedNarInfoHashes(ctx context.Context, db bun.IDB) ([]string, error) {
	var hashes []string

	err := db.NewSelect().Model(&NarInfo{}).Column("hash").Where("url IS NOT NULL").Scan(ctx, &hashes)

	return hashes, err
}

// GetUnmigratedNarInfoHashes returns hashes of NarInfos that have not been migrated (URL is NULL).
func GetUnmigratedNarInfoHashes(ctx context.Context, db bun.IDB) ([]string, error) {
	var hashes []string

	err := db.NewSelect().Model(&NarInfo{}).Column("hash").Where("url IS NULL").Scan(ctx, &hashes)

	return hashes, err
}

// GetNarInfosWithoutNarFiles returns NarInfos that don't have associated NarFiles.
func GetNarInfosWithoutNarFiles(ctx context.Context, db bun.IDB) ([]NarInfo, error) {
	var narInfos []NarInfo

	err := db.NewRaw(`
		SELECT n.* FROM narinfos n
		LEFT JOIN narinfo_nar_files nnf ON n.id = nnf.narinfo_id
		WHERE nnf.narinfo_id IS NULL
	`).Scan(ctx, &narInfos)

	return narInfos, err
}

// UpdateNarInfoCompressionAndURLParams holds parameters for updating compression and URL.
type UpdateNarInfoCompressionAndURLParams struct {
	Compression sql.NullString
	NewURL      sql.NullString
	OldURL      sql.NullString
}

// UpdateNarInfoCompressionAndURL updates compression and URL for matching NarInfos.
func UpdateNarInfoCompressionAndURL(
	ctx context.Context, db bun.IDB, arg UpdateNarInfoCompressionAndURLParams,
) (int64, error) {
	result, err := db.NewUpdate().Model(&NarInfo{}).
		Set("compression = ?", arg.Compression).
		Set("url = ?", arg.NewURL).
		Where("url = ?", arg.OldURL).
		Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// UpdateNarInfoCompressionFileSizeHashAndURLParams holds parameters.
type UpdateNarInfoCompressionFileSizeHashAndURLParams struct {
	Compression sql.NullString
	NewURL      sql.NullString
	FileSize    sql.NullInt64
	FileHash    sql.NullString
	OldURL      sql.NullString
}

// UpdateNarInfoCompressionFileSizeHashAndURL updates multiple fields.
func UpdateNarInfoCompressionFileSizeHashAndURL(
	ctx context.Context, db bun.IDB, arg UpdateNarInfoCompressionFileSizeHashAndURLParams,
) (int64, error) {
	result, err := db.NewUpdate().Model(&NarInfo{}).
		Set("compression = ?", arg.Compression).
		Set("url = ?", arg.NewURL).
		Set("file_size = ?", arg.FileSize).
		Set("file_hash = ?", arg.FileHash).
		Where("url = ?", arg.OldURL).
		Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// UpdateNarInfoFileHash updates the file_hash for a NarInfo.
func UpdateNarInfoFileHash(ctx context.Context, db bun.IDB, hash string, fileHash sql.NullString) error {
	_, err := db.NewUpdate().Model(&NarInfo{}).
		Set("file_hash = ?", fileHash).
		Where("hash = ?", hash).
		Exec(ctx)

	return err
}

// UpdateNarInfoFileSize updates the file_size for a NarInfo.
func UpdateNarInfoFileSize(ctx context.Context, db bun.IDB, hash string, fileSize sql.NullInt64) error {
	_, err := db.NewUpdate().Model(&NarInfo{}).
		Set("file_size = ?", fileSize).
		Where("hash = ?", hash).
		Exec(ctx)

	return err
}

// GetNarInfoHashByNarURL returns the NarInfo hash for a given NAR URL.
func GetNarInfoHashByNarURL(ctx context.Context, db bun.IDB, url sql.NullString) (string, error) {
	var hash string

	err := db.NewSelect().Model(&NarInfo{}).Column("hash").Where("url = ?", url).Scan(ctx, &hash)
	if err != nil {
		return "", err
	}

	return hash, nil
}

// GetNarInfoHashesByURL returns all NarInfo hashes for a given URL.
func GetNarInfoHashesByURL(ctx context.Context, db bun.IDB, url sql.NullString) ([]string, error) {
	var hashes []string

	err := db.NewSelect().Model(&NarInfo{}).Column("hash").Where("url = ?", url).Scan(ctx, &hashes)

	return hashes, err
}

// GetNarTotalSize returns the sum of all nar_sizes.
func GetNarTotalSize(ctx context.Context, db bun.IDB) (int64, error) {
	var totalSize int64

	err := db.NewRaw("SELECT COALESCE(SUM(nar_size), 0) FROM narinfos WHERE nar_size IS NOT NULL").Scan(ctx, &totalSize)

	return totalSize, err
}

// AddNarInfoReferenceParams holds parameters for adding a reference.
type AddNarInfoReferenceParams struct {
	NarInfoID int64
	Reference string
}

// AddNarInfoReference adds a reference to a NarInfo.
func AddNarInfoReference(ctx context.Context, db bun.IDB, arg AddNarInfoReferenceParams) error {
	ref := &NarInfoReference{
		NarInfoID: arg.NarInfoID,
		Reference: arg.Reference,
	}
	_, err := db.NewInsert().Model(ref).Ignore().Exec(ctx)

	return err
}

// AddNarInfoReferencesParams holds parameters for bulk adding references.
type AddNarInfoReferencesParams struct {
	NarInfoID int64
	Reference []string
}

// AddNarInfoReferences adds multiple references to a NarInfo.
func AddNarInfoReferences(ctx context.Context, db bun.IDB, arg AddNarInfoReferencesParams) error {
	for _, ref := range arg.Reference {
		narInfoRef := &NarInfoReference{
			NarInfoID: arg.NarInfoID,
			Reference: ref,
		}

		_, err := db.NewInsert().Model(narInfoRef).Ignore().Exec(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// AddNarInfoSignatureParams holds parameters for adding a signature.
type AddNarInfoSignatureParams struct {
	NarInfoID int64
	Signature string
}

// AddNarInfoSignature adds a signature to a NarInfo.
func AddNarInfoSignature(ctx context.Context, db bun.IDB, arg AddNarInfoSignatureParams) error {
	sig := &NarInfoSignature{
		NarInfoID: arg.NarInfoID,
		Signature: arg.Signature,
	}
	_, err := db.NewInsert().Model(sig).Ignore().Exec(ctx)

	return err
}

// AddNarInfoSignaturesParams holds parameters for bulk adding signatures.
type AddNarInfoSignaturesParams struct {
	NarInfoID int64
	Signature []string
}

// AddNarInfoSignatures adds multiple signatures to a NarInfo.
func AddNarInfoSignatures(ctx context.Context, db bun.IDB, arg AddNarInfoSignaturesParams) error {
	for _, sig := range arg.Signature {
		narInfoSig := &NarInfoSignature{
			NarInfoID: arg.NarInfoID,
			Signature: sig,
		}

		_, err := db.NewInsert().Model(narInfoSig).Ignore().Exec(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetNarInfoReferences returns all references for a NarInfo.
func GetNarInfoReferences(ctx context.Context, db bun.IDB, narinfoID int64) ([]string, error) {
	var refs []string

	err := db.NewSelect().Model(&NarInfoReference{}).
		Column("reference").
		Where("narinfo_id = ?", narinfoID).
		Scan(ctx, &refs)

	return refs, err
}

// GetNarInfoSignatures returns all signatures for a NarInfo.
func GetNarInfoSignatures(ctx context.Context, db bun.IDB, narinfoID int64) ([]string, error) {
	var sigs []string

	err := db.NewSelect().Model(&NarInfoSignature{}).
		Column("signature").
		Where("narinfo_id = ?", narinfoID).
		Scan(ctx, &sigs)

	return sigs, err
}

// LinkNarInfoToNarFileParams holds parameters for linking.
type LinkNarInfoToNarFileParams struct {
	NarInfoID int64
	NarFileID int64
}

// LinkNarInfoToNarFile creates a link between a NarInfo and NarFile.
func LinkNarInfoToNarFile(ctx context.Context, db bun.IDB, arg LinkNarInfoToNarFileParams) error {
	link := &NarInfoNarFile{
		NarInfoID: arg.NarInfoID,
		NarFileID: arg.NarFileID,
	}
	_, err := db.NewInsert().Model(link).Ignore().Exec(ctx)

	return err
}

// LinkNarInfosByURLToNarFileParams holds parameters.
type LinkNarInfosByURLToNarFileParams struct {
	NarFileID int64
	URL       sql.NullString
}

// LinkNarInfosByURLToNarFile links all NarInfos with a given URL to a NarFile.
func LinkNarInfosByURLToNarFile(ctx context.Context, db bun.IDB, arg LinkNarInfosByURLToNarFileParams) error {
	// Use INSERT ... SELECT to link all matching narinfos in a single query.
	// MySQL uses INSERT IGNORE; PostgreSQL/SQLite use ON CONFLICT DO NOTHING.
	var query string
	if db.Dialect().Name() == dialect.MySQL {
		query = "INSERT IGNORE INTO narinfo_nar_files (narinfo_id, nar_file_id) SELECT id, ? FROM narinfos WHERE url = ?"
	} else {
		query = "INSERT INTO narinfo_nar_files (narinfo_id, nar_file_id)" +
			" SELECT id, ? FROM narinfos WHERE url = ?" +
			" ON CONFLICT (narinfo_id, nar_file_id) DO NOTHING"
	}

	_, err := db.ExecContext(ctx, query, arg.NarFileID, arg.URL)

	return err
}
