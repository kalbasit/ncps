package database

import (
	"context"
	"database/sql"

	"github.com/kalbasit/ncps/pkg/database/postgresdb"
)

// postgresWrapper wraps a PostgreSQL adapter to provide type conversion.
type postgresWrapper struct {
	adapter *postgresdb.Adapter
}

func (w *postgresWrapper) CreateNarFile(ctx context.Context, arg CreateNarFileParams) (NarFile, error) {
	p := postgresdb.CreateNarFileParams{
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
		ID:             int64(narFile.ID),
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *postgresWrapper) CreateNarInfo(ctx context.Context, hash string) (NarInfo, error) {
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

func (w *postgresWrapper) DeleteNarFileByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarFileByHash(ctx, hash)
}

func (w *postgresWrapper) DeleteNarFileByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarFileByID(ctx, int32(id)) //nolint:gosec
}

func (w *postgresWrapper) DeleteNarInfoByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarInfoByHash(ctx, hash)
}

func (w *postgresWrapper) DeleteNarInfoByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarInfoByID(ctx, id)
}

func (w *postgresWrapper) DeleteOrphanedNarFiles(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarFiles(ctx)
}

func (w *postgresWrapper) DeleteOrphanedNarInfos(ctx context.Context) (int64, error) {
	return w.adapter.DeleteOrphanedNarInfos(ctx)
}

func (w *postgresWrapper) GetLeastUsedNarFiles(ctx context.Context, fileSize uint64) ([]NarFile, error) {
	narFiles, err := w.adapter.GetLeastUsedNarFiles(ctx, int64(fileSize)) //nolint:gosec
	if err != nil {
		return nil, err
	}

	result := make([]NarFile, len(narFiles))
	for i, n := range narFiles {
		result[i] = NarFile{
			ID:             int64(n.ID),
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

func (w *postgresWrapper) GetLeastUsedNarInfos(ctx context.Context, fileSize uint64) ([]NarInfo, error) {
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

func (w *postgresWrapper) GetNarFileByHash(ctx context.Context, hash string) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByHash(ctx, hash)
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             int64(narFile.ID),
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *postgresWrapper) GetNarFileByID(ctx context.Context, id int64) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByID(ctx, int32(id)) //nolint:gosec
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             int64(narFile.ID),
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *postgresWrapper) GetNarInfoHashesByNarFileID(ctx context.Context, narFileID int64) ([]string, error) {
	return w.adapter.GetNarInfoHashesByNarFileID(ctx, int32(narFileID)) //nolint:gosec
}

func (w *postgresWrapper) GetNarFileByNarInfoID(ctx context.Context, narinfoID int64) (NarFile, error) {
	narFile, err := w.adapter.GetNarFileByNarInfoID(ctx, int32(narinfoID)) //nolint:gosec
	if err != nil {
		return NarFile{}, err
	}

	return NarFile{
		ID:             int64(narFile.ID),
		Hash:           narFile.Hash,
		Compression:    narFile.Compression,
		FileSize:       uint64(narFile.FileSize), //nolint:gosec
		CreatedAt:      narFile.CreatedAt,
		UpdatedAt:      narFile.UpdatedAt,
		LastAccessedAt: narFile.LastAccessedAt,
		Query:          narFile.Query,
	}, nil
}

func (w *postgresWrapper) GetNarInfoByHash(ctx context.Context, hash string) (NarInfo, error) {
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

func (w *postgresWrapper) GetNarInfoByID(ctx context.Context, id int64) (NarInfo, error) {
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

func (w *postgresWrapper) GetNarTotalSize(ctx context.Context) (int64, error) {
	return w.adapter.GetNarTotalSize(ctx)
}

func (w *postgresWrapper) LinkNarInfoToNarFile(ctx context.Context, arg LinkNarInfoToNarFileParams) error {
	p := postgresdb.LinkNarInfoToNarFileParams{
		NarInfoID: int32(arg.NarInfoID), //nolint:gosec
		NarFileID: int32(arg.NarFileID), //nolint:gosec
	}

	return w.adapter.LinkNarInfoToNarFile(ctx, p)
}

func (w *postgresWrapper) TouchNarFile(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarFile(ctx, hash)
}

func (w *postgresWrapper) TouchNarInfo(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNarInfo(ctx, hash)
}

func (w *postgresWrapper) WithTx(tx *sql.Tx) Querier {
	return &postgresWrapper{adapter: w.adapter.WithTx(tx)}
}

func (w *postgresWrapper) DB() *sql.DB {
	return w.adapter.DB()
}
