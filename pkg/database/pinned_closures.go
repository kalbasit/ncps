package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

// CreatePinnedClosure inserts a new PinnedClosure.
func CreatePinnedClosure(ctx context.Context, db bun.IDB, hash string) (PinnedClosure, error) {
	pc := &PinnedClosure{
		Hash: hash,
	}

	if db.Dialect().Name() == dialect.MySQL {
		_, err := db.NewInsert().Model(pc).Exec(ctx)
		if err != nil {
			return PinnedClosure{}, err
		}

		return GetPinnedClosure(ctx, db, hash)
	}

	_, err := db.NewInsert().Model(pc).Returning("*").Exec(ctx, pc)
	if err != nil {
		return PinnedClosure{}, err
	}

	return *pc, nil
}

// GetPinnedClosure retrieves a PinnedClosure by hash.
func GetPinnedClosure(ctx context.Context, db bun.IDB, hash string) (PinnedClosure, error) {
	var pc PinnedClosure

	err := db.NewSelect().Model(&pc).Where("hash = ?", hash).Scan(ctx, &pc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PinnedClosure{}, ErrNotFound
		}

		return PinnedClosure{}, err
	}

	return pc, nil
}

// GetPinnedClosureByID retrieves a PinnedClosure by ID.
func GetPinnedClosureByID(ctx context.Context, db bun.IDB, id int64) (PinnedClosure, error) {
	var pc PinnedClosure

	err := db.NewSelect().Model(&pc).Where("id = ?", id).Scan(ctx, &pc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PinnedClosure{}, ErrNotFound
		}

		return PinnedClosure{}, err
	}

	return pc, nil
}

// DeletePinnedClosure deletes a PinnedClosure by hash.
func DeletePinnedClosure(ctx context.Context, db bun.IDB, hash string) (int64, error) {
	result, err := db.NewDelete().Model(&PinnedClosure{}).Where("hash = ?", hash).Exec(ctx)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// ListPinnedClosures returns all PinnedClosures.
func ListPinnedClosures(ctx context.Context, db bun.IDB) ([]PinnedClosure, error) {
	var pcs []PinnedClosure

	err := db.NewSelect().Model(&pcs).Scan(ctx, &pcs)

	return pcs, err
}
