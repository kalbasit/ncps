package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/sqlitedb"
)

// sqliteWrapper wraps a SQLite adapter to provide type conversion.
type sqliteWrapper struct {
	adapter *sqlitedb.Adapter
}

func (w *sqliteWrapper) CreateNarFile(ctx context.Context, arg CreateNarFileParams) (NarFile, error) {
	p := sqlitedb.CreateNarFileParams{
		Hash:        arg.Hash,
		Compression: arg.Compression,
		Query:       arg.Query,
		FileSize:    int64(arg.FileSize), //nolint:gosec
	}

	narFile, err := w.adapter.CreateNarFile(ctx, p)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             narFile.ID,
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *sqliteWrapper) CreateNarInfo(ctx context.Context, hash string) (NarInfo, error) {
	ni, err := w.adapter.CreateNarInfo(ctx, hash)
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

func (w *sqliteWrapper) DeleteNarFileByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarFileByHash(ctx, hash)
}

func (w *sqliteWrapper) DeleteNarFileByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarFileByID(ctx, id)
}

func (w *sqliteWrapper) DeleteNarInfoByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarInfoByHash(ctx, hash)
}

func (w *sqliteWrapper) DeleteNarInfoByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarInfoByID(ctx, id)
}

func (w *sqliteWrapper) DeleteOrphanedNarFiles(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarFiles(ctx)
}

func (w *sqliteWrapper) DeleteOrphanedNarInfos(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarInfos(ctx)
}

func (w *sqliteWrapper) GetLeastUsedNarFiles(ctx context.Context, fileSize uint64) ([]NarFile, error) {
	narFiles, err := w.adapter.GetLeastUsedNarFiles(ctx, int64(fileSize)) //nolint:gosec
	if err != nil {
		return nil, err
	}

	result := make([]NarFile, len(narFiles))
	for i, n := range narFiles {
		result[i] = NarFile{
			ID:             n.ID,
			Hash:           n.Hash,
			Compression:    n.Compression,
			FileSize:       uint64(n.FileSize), //nolint:gosec
			CreatedAt:      n.CreatedAt,
			UpdatedAt:      n.UpdatedAt,
			LastAccessedAt: n.LastAccessedAt,
			Query:          n.Query,
		}
	}

	return result, nil
}

func (w *sqliteWrapper) GetLeastUsedNarInfos(ctx context.Context, fileSize uint64) ([]NarInfo, error) {
	narInfos, err := w.adapter.GetLeastUsedNarInfos(ctx, int64(fileSize)) //nolint:gosec
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

func (w *sqliteWrapper) GetNarFileByHash(ctx context.Context, hash string) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByHash(ctx, hash)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             narFile.ID,
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *sqliteWrapper) GetNarFileByID(ctx context.Context, id int64) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByID(ctx, id)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             narFile.ID,
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *sqliteWrapper) GetNarInfoHashesByNarFileID(ctx context.Context, narFileID int64) ([]string, error) {
	return w.adapter.GetNarInfoHashesByNarFileID(ctx, narFileID)
}

func (w *sqliteWrapper) GetNarFileByNarInfoID(ctx context.Context, narinfoID int64) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByNarInfoID(ctx, narinfoID)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             narFile.ID,
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *sqliteWrapper) GetNarInfoByHash(ctx context.Context, hash string) (NarInfo, error) {
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

func (w *sqliteWrapper) GetNarInfoByID(ctx context.Context, id int64) (NarInfo, error) {
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

func (w *sqliteWrapper) GetNarTotalSize(ctx context.Context) (int64, error) {
	return w.adapter.GetNarTotalSize(ctx)
}

func (w *sqliteWrapper) LinkNarInfoToNarFile(ctx context.Context, arg LinkNarInfoToNarFileParams) error {
	p := sqlitedb.LinkNarInfoToNarFileParams{
		NarInfoID: arg.NarInfoID,
		NarFileID: arg.NarFileID,
	}

	return w.adapter.LinkNarInfoToNarFile(ctx, p)
}

func (w *sqliteWrapper) TouchNarFile(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarFile(ctx, hash)
}

func (w *sqliteWrapper) TouchNarInfo(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarInfo(ctx, hash)
}

func (w *sqliteWrapper) WithTx(tx *sql.Tx) Querier {
	return &sqliteWrapper{adapter: w.adapter.WithTx(tx)}
}

func (w *sqliteWrapper) DB() *sql.DB {
	return w.adapter.DB()
}
