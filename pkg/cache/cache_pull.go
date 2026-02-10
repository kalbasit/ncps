package cache

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nix-community/go-nix/pkg/narinfo"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nar"
)

func (c *Cache) getNarInfoForPull(
	ctx context.Context,
	narURL *nar.URL,
	uc *upstream.Cache,
	narInfo *narinfo.NarInfo,
) (*upstream.Cache, *narinfo.NarInfo, error) {
	// 1. If narInfo is provided, we use it.
	if narInfo != nil {
		return uc, narInfo, nil
	}

	// 2. Try to find the narinfo hash for this NAR from our database
	// This handles cases where GetNar is called directly for a NAR we know about but don't have.
	// We use the normalized URL because that's what we store in the database.
	normalizedURL := narURL.Normalize()

	hashes, err := c.db.GetNarInfoHashesByURL(ctx, sql.NullString{String: normalizedURL.String(), Valid: true})
	if err == nil && len(hashes) > 0 {
		// Use the first narinfo we find
		hash := hashes[0]

		uc, narInfo, err := c.getNarInfoFromUpstream(ctx, hash)
		if err == nil {
			return uc, narInfo, nil
		}
	}

	// 3. Last resort: Try to fetch from upstream using the hash in the URL.
	// This usually only works if the hash in the URL IS the narinfo hash (which nix-serve supports).
	uc, narInfo, err = c.getNarInfoFromUpstream(ctx, narURL.Hash)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch narinfo for hash %s: %w", narURL.Hash, err)
	}

	return uc, narInfo, nil
}
