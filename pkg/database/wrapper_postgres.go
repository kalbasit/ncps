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

func (w *postgresWrapper) CreateNar(ctx context.Context, arg CreateNarParams) (Nar, error) {
	p := postgresdb.CreateNarParams{
		NarInfoID:   arg.NarInfoID,
		Hash:        arg.Hash,
		Compression: arg.Compression,
		Query:       arg.Query,
		FileSize:    arg.FileSize,
	}

	nar, err := w.adapter.CreateNar(ctx, p)
	if err != nil {
		return Nar{}, err
	}

	return Nar{
		ID:             nar.ID,
		NarInfoID:      nar.NarInfoID,
		Hash:           nar.Hash,
		Compression:    nar.Compression,
		FileSize:       nar.FileSize,
		CreatedAt:      nar.CreatedAt,
		UpdatedAt:      nar.UpdatedAt,
		LastAccessedAt: nar.LastAccessedAt,
		Query:          nar.Query,
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

func (w *postgresWrapper) DeleteNarByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarByHash(ctx, hash)
}

func (w *postgresWrapper) DeleteNarByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarByID(ctx, id)
}

func (w *postgresWrapper) DeleteNarInfoByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarInfoByHash(ctx, hash)
}

func (w *postgresWrapper) DeleteNarInfoByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarInfoByID(ctx, id)
}

func (w *postgresWrapper) GetLeastUsedNars(ctx context.Context, fileSize uint64) ([]Nar, error) {
	nars, err := w.adapter.GetLeastUsedNars(ctx, fileSize)
	if err != nil {
		return nil, err
	}

	result := make([]Nar, len(nars))
	for i, n := range nars {
		result[i] = Nar{
			ID:             n.ID,
			NarInfoID:      n.NarInfoID,
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

func (w *postgresWrapper) GetNarByHash(ctx context.Context, hash string) (Nar, error) {
	nar, err := w.adapter.GetNarByHash(ctx, hash)
	if err != nil {
		return Nar{}, err
	}

	return Nar{
		ID:             nar.ID,
		NarInfoID:      nar.NarInfoID,
		Hash:           nar.Hash,
		Compression:    nar.Compression,
		FileSize:       nar.FileSize,
		CreatedAt:      nar.CreatedAt,
		UpdatedAt:      nar.UpdatedAt,
		LastAccessedAt: nar.LastAccessedAt,
		Query:          nar.Query,
	}, nil
}

func (w *postgresWrapper) GetNarByID(ctx context.Context, id int64) (Nar, error) {
	nar, err := w.adapter.GetNarByID(ctx, id)
	if err != nil {
		return Nar{}, err
	}

	return Nar{
		ID:             nar.ID,
		NarInfoID:      nar.NarInfoID,
		Hash:           nar.Hash,
		Compression:    nar.Compression,
		FileSize:       nar.FileSize,
		CreatedAt:      nar.CreatedAt,
		UpdatedAt:      nar.UpdatedAt,
		LastAccessedAt: nar.LastAccessedAt,
		Query:          nar.Query,
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

func (w *postgresWrapper) TouchNar(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNar(ctx, hash)
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
