package cache

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

// generateIndex rebuilds the entire cache index (manifest and shards) from the database.
func (c *Cache) generateIndex() {
	ctx := c.baseContext
	logger := zerolog.Ctx(ctx).With().Str("component", "index_generator").Logger()
	ctx = logger.WithContext(ctx)

	logger.Info().Msg("starting cache index generation")

	start := time.Now()

	if err := c.doGenerateIndex(ctx); err != nil {
		logger.Error().Err(err).Msg("failed to generate cache index")
	} else {
		logger.Info().Dur("duration", time.Since(start)).Msg("cache index generation completed successfully")
	}
}

func (c *Cache) doGenerateIndex(ctx context.Context) error {
	// 1. Fetch all NarInfo hashes from DB
	rows, err := c.db.GetAllNarInfos(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch all narinfos: %w", err)
	}

	// 2. Prepare Manifest
	// For now, we recreate a fresh manifest.
	// TODO: Load existing manifest to preserve journal/version?
	// But we are doing a full rebuild, so a fresh manifest might be appropriate.
	manifest := nixcacheindex.NewManifest()

	scheme := "http"
	if c.experimentalCacheIndexHTTPS {
		scheme = "https"
	}

	baseURL := fmt.Sprintf("%s://%s/nix-cache-index/", scheme, c.hostName)
	manifest.Urls.JournalBase = baseURL + "journal/"
	manifest.Urls.ShardsBase = baseURL + "shards/"
	manifest.Urls.DeltasBase = baseURL + "deltas/"

	// 3. Process Hashes
	type Item struct {
		Big *big.Int
		Str string
	}

	items := make([]Item, 0, len(rows))
	for _, hash := range rows {
		h, err := nixcacheindex.ParseHash(hash)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Str("hash", hash).Err(err).Msg("skipping invalid hash")

			continue
		}

		items = append(items, Item{Big: h, Str: hash})
	}

	// 4. Sort Hashes
	sort.Slice(items, func(i, j int) bool {
		return items[i].Big.Cmp(items[j].Big) < 0
	})

	manifest.ItemCount = int64(len(items))

	// 5. Build Shards
	shards := make(map[string][]*big.Int)
	depth := manifest.Sharding.Depth

	if depth == 0 {
		list := make([]*big.Int, len(items))
		for i, item := range items {
			list[i] = item.Big
		}

		shards["root"] = list
	} else {
		for _, item := range items {
			if len(item.Str) < depth {
				continue
			}

			prefix := item.Str[:depth]
			shards[prefix] = append(shards[prefix], item.Big)
		}
	}

	// 6. Write Shards to Store
	epoch := manifest.Epoch.Current

	for name, list := range shards {
		var buf bytes.Buffer

		// Encode
		params := manifest.Encoding
		// IMPORTANT: nixcacheindex.WriteShard expects hashes to be sorted. They are.

		// Compress with zstd and write shard directly to the compressor.
		enc, err := zstd.NewWriter(&buf)
		if err != nil {
			return fmt.Errorf("failed to create zstd writer: %w", err)
		}

		writeErr := nixcacheindex.WriteShard(enc, list, params)
		closeErr := enc.Close()

		if writeErr != nil {
			return fmt.Errorf("failed to write shard %s: %w", name, writeErr)
		}

		if closeErr != nil {
			return fmt.Errorf("failed to close zstd writer for shard %s: %w", name, closeErr)
		}

		path := fmt.Sprintf("/nix-cache-index/shards/%d/%s.idx.zst", epoch, name)
		if _, err := c.fileStore.PutFile(ctx, path, &buf); err != nil {
			return fmt.Errorf("failed to store shard file %s: %w", path, err)
		}
	}

	// 7. Write Manifest
	var buf bytes.Buffer
	if err := manifest.Write(&buf); err != nil {
		return fmt.Errorf("failed to encode manifest: %w", err)
	}

	if _, err := c.fileStore.PutFile(ctx, nixcacheindex.ManifestPath, &buf); err != nil {
		return fmt.Errorf("failed to store manifest: %w", err)
	}

	return nil
}
