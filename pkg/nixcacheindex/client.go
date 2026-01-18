package nixcacheindex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog"
)

// Result of a query.
type Result int

const (
	DefiniteMiss Result = iota
	DefiniteHit
	ProbableHit
)

func (r Result) String() string {
	switch r {
	case DefiniteMiss:
		return "DEFINITE_MISS"
	case DefiniteHit:
		return "DEFINITE_HIT"
	case ProbableHit:
		return "PROBABLE_HIT"
	default:
		return fmt.Sprintf("Result(%d)", int(r))
	}
}

// Fetcher abstraction for retrieving files (e.g., HTTP, local file system).
type Fetcher interface {
	Fetch(path string) (io.ReadCloser, error)
}

// Client for querying the binary cache index.
type Client struct {
	fetcher  Fetcher
	manifest *Manifest

	shardCacheMu sync.Mutex
	shardCache   map[string]*ShardReader
}

// NewClient creates a new client.
func NewClient(fetcher Fetcher) *Client {
	return &Client{
		fetcher:    fetcher,
		shardCache: make(map[string]*ShardReader),
	}
}

// LoadManifest fetches and parses the manifest.
func (c *Client) LoadManifest() error {
	r, err := c.fetcher.Fetch(ManifestPath)
	if err != nil {
		return err
	}
	defer r.Close()

	m, err := LoadManifest(r)
	if err != nil {
		return err
	}

	c.manifest = m

	return nil
}

// Query checks if the cache contains the given hash.
// hashStr is the 32-character base32 hash.
func (c *Client) Query(ctx context.Context, hashStr string) (Result, error) {
	if c.manifest == nil {
		if err := c.LoadManifest(); err != nil {
			return DefiniteMiss, err
		}
	}

	// 1. Check Journal (Layer 1)
	// Iterate through active segments.
	// RFC: "Check journal for recent mutations"
	// "segments_to_compact = get_segments_older_than..."
	// Need to know WHICH segments to check.
	// Manifest says `journal.current_segment`.
	// RFC says: "Writer appends to current... Segments older than retention...".
	// So we should check `current_segment` and previous segments?
	// RFC Section 7 Step 2: "FOR segment IN manifest.journal.segments".
	// Manifest JSON example doesn't have "segments" list. It has `current_segment`, `segment_duration`, `retention`.
	// We infer the segments: [current, current-duration, current-2*duration ... up to retention].

	current := c.manifest.Journal.CurrentSegment
	duration := int64(c.manifest.Journal.SegmentDurationSeconds)
	count := c.manifest.Journal.RetentionCount

	// We check newest to oldest? Order doesn't matter for "set" semantics, but strictly speaking
	// if something was Added then Deleted, order matters.
	// Journal text format preserves order.
	// Across files?
	// "Segments older... are archived".
	// If we have strict linearization, we should check segments in chronological order?
	// And within a segment, order matters.
	// "Lines beginning with - indicate deletions".
	// "On artifact push: Append +hash".
	// "On GC: Append -hash".
	// If we see -hash (deleted), we return DEFINITE_MISS.
	// If we see +hash (added), we return PROBABLE_HIT.
	// What if -hash comes AFTER +hash? (deleted).
	// What if +hash comes AFTER -hash? (re-added? unlikely for immutable store path, but possible if GC'd then re-pushed).
	// So we need to process all journal entries in Chronological order to find the FINAL state?
	// Or just look for ANY usage?
	// RFC Query Algo:
	// "IF "-" + target IN journal: RETURN MISS"
	// "IF "+" + target IN journal: RETURN PROBABLE_HIT"
	// This implies checking all journals together? Or order?
	// If I see a deletion 5 mins ago, and addition 1 min ago. It's present.
	// If checking in Reverse Chronological (Newest first):
	//   If I see +hash: It's present (ignoring older deletion). Return PROBABLE_HIT.
	//   If I see -hash: It's deleted (ignoring older addition). Return DEFINITE_MISS.
	// So Reverse Chronological check is correct and efficient.

	// Generate segment timestamps (start times)
	// Current is start time of current segment.

	segments := make([]int64, 0, count+1)
	for i := 0; i < count+1; i++ { // +1 for current? "retention_count ... segments retained before archival".
		// Usually retention doesn't include current?
		// RFC Example 1: `retention_count: 24`, `current`: 1705147200.
		// Files: `1705147200.log` (current). Previous ones...
		// We will check `current` and `count` previous ones.
		t := current - int64(i)*duration
		segments = append(segments, t)
	}

	// Check segments (Newest first)
	targetHash := hashStr // We look for strict string match? RFC says yes "line[1:]".

	for _, segTime := range segments {
		path := fmt.Sprintf("%s%d.log", c.manifest.Urls.JournalBase, segTime)

		// Fetch journal
		// Note: fetcher should handle caching or 404s (empty journal?)
		// Fetcher.Fetch returns error if not found?
		// If 404, we assume empty/ignore?
		// RFC says "Fetch journal...".
		// In reality, some segments might not exist if no writes happened?
		// Or if rotation logic is strict.

		rc, err := c.fetcher.Fetch(path)
		if err != nil {
			// If journal segment is missing, we assume no mutations in that window.
			zerolog.Ctx(ctx).Debug().Err(err).Str("path", path).
				Msg("journal segment missing, assuming no mutations in this window")

			continue
		}
		defer rc.Close()

		entries, err := ParseJournal(rc)
		if err != nil {
			// Bad journal. Ignore?
			return DefiniteMiss, fmt.Errorf("failed to parse journal %s: %w", path, err)
		}

		// Search entries in Reverse Order (Newest lines are at bottom)
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Hash == targetHash {
				if e.Op == OpDelete {
					return DefiniteMiss, nil
				}

				if e.Op == OpAdd {
					return ProbableHit, nil
				}
			}
		}
	}

	// 2. Check Shards (Layer 2)
	// Determine Shard Path
	// prefix = target_hash[0:depth]
	depth := c.manifest.Sharding.Depth

	var prefix string
	if depth > 0 && len(hashStr) >= depth {
		prefix = hashStr[:depth]
	}

	epoch := c.manifest.Epoch.Current

	// Path: /nix-cache-index/shards/<epoch>/<prefix>.idx
	// If depth=0, prefix is usually "root" or empty?
	// RFC Example 1: `depth: 0`. Path: `shards/3/root.idx`.
	// RFC 5: "For sharding.depth = 0 ... shards/42/root.idx".
	// For depth > 0: `shards/42/b6.idx`.

	var shardName string
	if depth == 0 {
		shardName = "root"
	} else {
		shardName = prefix
	}

	shardPath := fmt.Sprintf("%s%d/%s.idx.zst", c.manifest.Urls.ShardsBase, epoch, shardName)

	c.shardCacheMu.Lock()
	shardReader, ok := c.shardCache[shardPath]
	c.shardCacheMu.Unlock()

	if ok {
		return c.queryShard(shardReader, hashStr)
	}

	rc, err := c.fetcher.Fetch(shardPath)
	if err == nil {
		defer rc.Close()

		return c.processShardResponse(shardPath, rc, hashStr)
	}

	// Shard missing?
	// RFC 9.2: "If shard fetch returns 404 AND epoch.previous exists... retry previous".
	if c.manifest.Epoch.Previous == 0 {
		return DefiniteMiss, nil // Missing shard -> Miss
	}

	prevEpoch := c.manifest.Epoch.Previous
	shardPath = fmt.Sprintf("%s%d/%s.idx.zst", c.manifest.Urls.ShardsBase, prevEpoch, shardName)

	c.shardCacheMu.Lock()
	shardReader, ok = c.shardCache[shardPath]
	c.shardCacheMu.Unlock()

	if ok {
		return c.queryShard(shardReader, hashStr)
	}

	rc, err = c.fetcher.Fetch(shardPath)
	if err != nil {
		return DefiniteMiss, err // Both missing -> Miss (or error)
	}
	defer rc.Close()

	return c.processShardResponse(shardPath, rc, hashStr)
}

func (c *Client) processShardResponse(shardPath string, rc io.ReadCloser, hashStr string) (Result, error) {
	// We assume Fetcher returns a Seekable stream needed for ReadShard?
	// fetcher.Fetch returns io.ReadCloser.
	// ReadShard needs io.ReadSeeker.
	// If fetcher is HTTP, it might not be seekable.
	// We might need to ReadAll into memory.
	// For shards (small/medium), this is fine (hundreds of KB).
	// For large shards (1MB+), memory is still fine.

	// Buffer it.
	// Note: This is an optimization point (Range requests).
	// For now, read all.
	var reader io.Reader = rc
	if strings.HasSuffix(shardPath, ".zst") {
		zstdReader, err := zstd.NewReader(rc)
		if err != nil {
			return DefiniteMiss, fmt.Errorf("failed to create zstd reader for %s: %w", shardPath, err)
		}
		defer zstdReader.Close()

		reader = zstdReader
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return DefiniteMiss, err
	}

	shardReader, err := ReadShard(bytes.NewReader(data))
	if err != nil {
		return DefiniteMiss, err
	}

	c.shardCacheMu.Lock()
	c.shardCache[shardPath] = shardReader
	c.shardCacheMu.Unlock()

	return c.queryShard(shardReader, hashStr)
}

func (c *Client) queryShard(shardReader *ShardReader, hashStr string) (Result, error) {
	// Parse Hash
	h, err := ParseHash(hashStr)
	if err != nil {
		return DefiniteMiss, fmt.Errorf("invalid hash: %w", err)
	}

	hit, err := shardReader.Contains(h)
	if err != nil {
		return DefiniteMiss, err
	}

	if hit {
		return DefiniteHit, nil
	}

	return DefiniteMiss, nil
}
