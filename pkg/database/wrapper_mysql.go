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

func (w *mysqlWrapper) CreateNar(ctx context.Context, arg CreateNarParams) (Nar, error) {
	// Convert to MySQL-specific params
	p := mysqldb.CreateNarParams{
		NarInfoID:   arg.NarInfoID,
		Hash:        arg.Hash,
		Compression: arg.Compression,
		Query:       arg.Query,
		FileSize:    arg.FileSize,
	}

	result, err := w.adapter.CreateNar(ctx, p)
	if err != nil {
		return Nar{}, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Nar{}, err
	}

	// Fetch the created record
	return w.GetNarByID(ctx, id)
}

func (w *mysqlWrapper) GetNarByHash(ctx context.Context, hash string) (Nar, error) {
	n, err := w.adapter.GetNarByHash(ctx, hash)
	if err != nil {
		return Nar{}, err
	}

	return Nar{
		ID:             n.ID,
		NarInfoID:      n.NarInfoID,
		Hash:           n.Hash,
		Compression:    n.Compression,
		FileSize:       n.FileSize,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		LastAccessedAt: n.LastAccessedAt,
		Query:          n.Query,
	}, nil
}

func (w *mysqlWrapper) GetNarByID(ctx context.Context, id int64) (Nar, error) {
	n, err := w.adapter.GetNarByID(ctx, id)
	if err != nil {
		return Nar{}, err
	}

	return Nar{
		ID:             n.ID,
		NarInfoID:      n.NarInfoID,
		Hash:           n.Hash,
		Compression:    n.Compression,
		FileSize:       n.FileSize,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		LastAccessedAt: n.LastAccessedAt,
		Query:          n.Query,
	}, nil
}

func (w *mysqlWrapper) DeleteNarByHash(ctx context.Context, hash string) (int64, error) {
	return w.adapter.DeleteNarByHash(ctx, hash)
}

func (w *mysqlWrapper) DeleteNarByID(ctx context.Context, id int64) (int64, error) {
	return w.adapter.DeleteNarByID(ctx, id)
}

func (w *mysqlWrapper) GetLeastUsedNars(ctx context.Context, fileSize uint64) ([]Nar, error) {
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

func (w *mysqlWrapper) TouchNar(ctx context.Context, hash string) (int64, error) {
	return w.adapter.TouchNar(ctx, hash)
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
