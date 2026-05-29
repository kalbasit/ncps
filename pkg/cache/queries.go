package cache

import (
	"context"
	"database/sql"

	entchunk "github.com/kalbasit/ncps/ent/chunk"
	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/ent"
)

// narInfoByHash returns the single narinfo row with the given hash. q may
// be either c.dbClient.Ent().NarInfo or a transaction's tx.NarInfo — both
// are *ent.NarInfoClient — so the same helper serves transactional and
// non-transactional callers. When no row matches it returns an error for
// which ent.IsNotFound reports true, identical to the inline
// Query().Where(HashEQ).Only(ctx) it replaces.
func narInfoByHash(ctx context.Context, q *ent.NarInfoClient, hash string) (*ent.NarInfo, error) {
	return q.Query().Where(entnarinfo.HashEQ(hash)).Only(ctx)
}

// chunksByHashes returns every chunk row whose hash is in hashes. q may be
// a client or transaction chunk client.
func chunksByHashes(ctx context.Context, q *ent.ChunkClient, hashes []string) ([]*ent.Chunk, error) {
	return q.Query().Where(entchunk.HashIn(hashes...)).All(ctx)
}

// totalNarFileSize returns the sum of file_size across all nar_files rows,
// or 0 when the table is empty (or the SUM is SQL NULL). It performs no
// logging; callers apply their own error-handling policy.
func totalNarFileSize(ctx context.Context, q *ent.NarFileClient) (int64, error) {
	var rows []struct {
		Sum sql.NullInt64 `sql:"sum"`
	}

	if err := q.Query().
		Aggregate(ent.Sum(entnarfile.FieldFileSize)).
		Scan(ctx, &rows); err != nil {
		return 0, err
	}

	if len(rows) > 0 && rows[0].Sum.Valid {
		return rows[0].Sum.Int64, nil
	}

	return 0, nil
}
