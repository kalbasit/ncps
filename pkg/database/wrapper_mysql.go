package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/mysqldb"
)

// mysqlWrapper wraps MySQL adapter to implement Querier interface.
type mysqlWrapper struct {
	adapter *mysqldb.Adapter
}

func (w *mysqlWrapper) CreateNarInfo(ctx context.Context, hash string) (NarInfo, error) {
	result, err := w.adapter.CreateNarInfo(ctx, hash)
	if err != nil {
		return NarInfo{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return NarInfo{}, err
	}

	// Fetch the created record
	return w.GetNarInfoByID(ctx, id)
}

func (w *mysqlWrapper) CreateNarFile(ctx context.Context, arg CreateNarFileParams) (NarFile, error) {
	// Convert to MySQL-specific params
	p := mysqldb.CreateNarFileParams{
		Hash:        arg.Hash,
		Compression: arg.Compression,
		Query:       arg.Query,
		FileSize:    arg.FileSize,
	}

	result, err := w.adapter.CreateNarFile(ctx, p)
	if err != nil {
		return NarFile{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return NarFile{}, err
	}

	// Fetch the created record
	return w.GetNarFileByID(ctx, id)
}

func (w *mysqlWrapper) GetNarFileByHash(ctx context.Context, hash string) (NarFile, error) {
	n, err := w.adapter.GetNarFileByHash(ctx, hash)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             n.ID,
		Hash:           n.Hash,
		Compression:    n.Compression,
		FileSize:       n.FileSize,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		LastAccessedAt: n.LastAccessedAt,
		Query:          n.Query,
	}, nil
}

func (w *mysqlWrapper) GetNarFileByID(ctx context.Context, id int64) (NarFile, error) {
	n, err := w.adapter.GetNarFileByID(ctx, id)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             n.ID,
		Hash:           n.Hash,
		Compression:    n.Compression,
		FileSize:       n.FileSize,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		LastAccessedAt: n.LastAccessedAt,
		Query:          n.Query,
	}, nil
}

func (w *mysqlWrapper) GetNarInfoHashesByNarFileID(ctx context.Context, narFileID int64) ([]string, error) {
	return w.adapter.GetNarInfoHashesByNarFileID(ctx, narFileID)
}

func (w *mysqlWrapper) GetNarFileByNarInfoID(ctx context.Context, narinfoID int64) (NarFile, error) {
	n, err := w.adapter.GetNarFileByNarInfoID(ctx, narinfoID)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             n.ID,
		Hash:           n.Hash,
		Compression:    n.Compression,
		FileSize:       n.FileSize,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		LastAccessedAt: n.LastAccessedAt,
		Query:          n.Query,
	}, nil
}

func (w *mysqlWrapper) DeleteNarFileByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarFileByHash(ctx, hash)
}

func (w *mysqlWrapper) DeleteNarFileByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarFileByID(ctx, id)
}

func (w *mysqlWrapper) DeleteOrphanedNarFiles(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarFiles(ctx)
}

func (w *mysqlWrapper) DeleteOrphanedNarInfos(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarInfos(ctx)
}

func (w *mysqlWrapper) GetLeastUsedNarFiles(ctx context.Context, fileSize uint64) ([]NarFile, error) {
	narFiles, err := w.adapter.GetLeastUsedNarFiles(ctx, fileSize)
	if err != nil {
		return nil, err
	}

	result := make([]NarFile, len(narFiles))
	for i, n := range narFiles {
		result[i] = NarFile{
			ID:             n.ID,
			Hash:           n.Hash,
			Compression:    n.Compression,
			FileSize:       n.FileSize,
			CreatedAt:      n.CreatedAt,
			UpdatedAt:      n.UpdatedAt,
			LastAccessedAt: n.LastAccessedAt,
			Query:          n.Query,
		}
	}

	return result, nil
}

func (w *mysqlWrapper) GetLeastUsedNarInfos(ctx context.Context, fileSize uint64) ([]NarInfo, error) {
	narInfos, err := w.adapter.GetLeastUsedNarInfos(ctx, fileSize)
	if err != nil {
		return nil, err
	}

	result := make([]NarInfo, len(narInfos))
	for i, n := range narInfos {
		result[i] = NarInfo{
			ID:             n.ID,
			Hash:           n.Hash,
			CreatedAt:      n.CreatedAt,
			UpdatedAt:      n.UpdatedAt,
			LastAccessedAt: n.LastAccessedAt,
		}
	}

	return result, nil
}

func (w *mysqlWrapper) DeleteNarInfoByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarInfoByHash(ctx, hash)
}

func (w *mysqlWrapper) DeleteNarInfoByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarInfoByID(ctx, id)
}

func (w *mysqlWrapper) GetNarInfoByHash(ctx context.Context, hash string) (NarInfo, error) {
	ni, err := w.adapter.GetNarInfoByHash(ctx, hash)
	if err != nil {
		return NarInfo{}, err
	}

	return NarInfo{
		ID:             ni.ID,
		Hash:           ni.Hash,
		CreatedAt:      ni.CreatedAt,
		UpdatedAt:      ni.UpdatedAt,
		LastAccessedAt: ni.LastAccessedAt,
	}, nil
}

func (w *mysqlWrapper) GetNarInfoByID(ctx context.Context, id int64) (NarInfo, error) {
	ni, err := w.adapter.GetNarInfoByID(ctx, id)
	if err != nil {
		return NarInfo{}, err
	}

	return NarInfo{
		ID:             ni.ID,
		Hash:           ni.Hash,
		CreatedAt:      ni.CreatedAt,
		UpdatedAt:      ni.UpdatedAt,
		LastAccessedAt: ni.LastAccessedAt,
	}, nil
}

func (w *mysqlWrapper) GetNarTotalSize(ctx context.Context) (int64, error) {
	return w.adapter.GetNarTotalSize(ctx)
}

func (w *mysqlWrapper) GetOrphanedNarInfoHashes(ctx context.Context) ([]string, error) {
	return w.adapter.GetOrphanedNarInfoHashes(ctx)
}

func (w *mysqlWrapper) LinkNarInfoToNarFile(ctx context.Context, arg LinkNarInfoToNarFileParams) error {
	p := mysqldb.LinkNarInfoToNarFileParams{
		NarInfoID: arg.NarInfoID,
		NarFileID: arg.NarFileID,
	}

	return w.adapter.LinkNarInfoToNarFile(ctx, p)
}

func (w *mysqlWrapper) TouchNarFile(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarFile(ctx, hash)
}

func (w *mysqlWrapper) TouchNarInfo(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarInfo(ctx, hash)
}

func (w *mysqlWrapper) WithTx(tx *sql.Tx) Querier {
	return &mysqlWrapper{adapter: w.adapter.WithTx(tx)}
}

func (w *mysqlWrapper) DB() *sql.DB {
	return w.adapter.DB()
}
