package cache

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

// noChunkStoreTestCache builds a Cache backed by a real local store with NO chunk
// store configured and CDC disabled — the configuration of a plain (non-CDC)
// deployment. This is the environment in which the stale-hasInStore TOCTOU
// surfaces as "chunk store not initialized".
func noChunkStoreTestCache(t *testing.T) *Cache {
	t.Helper()

	ctx := newContext()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })

	localStore, err := local.New(ctx, dir)
	require.NoError(t, err)

	c, err := New(ctx, cacheName, dbClient, localStore, localStore, localStore, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	// Intentionally NO SetChunkStore / SetCDCConfiguration: chunk store stays nil,
	// CDC stays disabled.
	return c
}

// TestServeNarStaleHasInStoreServesWholeFile reproduces the flaky failure observed
// in TestCacheBackends/.../RunLRUCleanupInconsistentNarInfoState
// ("chunk store not initialized, cannot serve NAR from chunks").
//
// GetNar reads hasNarInStore once and then re-evaluates servability via
// isServable, which performs its own fresh HasNarInStore check. When the
// whole-file NAR lands in the store between those two checks, the first flag is
// stale (false) while the NAR is in fact present. The stale flag is passed to
// serveNarFromStorageViaPipe, where serveFromChunks := !hasInStore routes an
// uncompressed request to getNarFromChunks — which hard-fails because no chunk
// store is configured. The serve path MUST serve the present whole file instead.
func TestServeNarStaleHasInStoreServesWholeFile(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	const hash = "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc28"

	c := noChunkStoreTestCache(t)

	content := strings.Repeat("stale has-in-store content; ", 256)
	nu := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}
	require.NoError(t, c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content))))
	require.True(t, c.HasNarInStore(ctx, nu), "whole file must be present in the store")

	// hasInStore=false models the stale time-of-check: the whole file was observed
	// absent before it landed. With no chunk store, the serve path must NOT route
	// to chunks; it must serve the now-present whole file.
	size, rc, err := c.serveNarFromStorageViaPipe(ctx, &nu, false)
	require.NoError(t, err,
		"a stale not-in-store observation must not route an uncompressed serve to an absent chunk store")
	require.NotNil(t, rc)

	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, content, string(got))
	require.Equal(t, int64(len(content)), size)
}

// TestServeNarStaleHasInStoreGenuinelyAbsentReturnsNotFound asserts that gating
// the chunk route on chunk-store availability preserves not-found semantics: with
// no chunk store and no whole file, an uncompressed serve resolves against the
// store and returns storage.ErrNotFound — never the "chunk store not initialized"
// error — so the caller's cache-miss recovery (re-download) still engages.
func TestServeNarStaleHasInStoreGenuinelyAbsentReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	const hash = "11ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc28"

	c := noChunkStoreTestCache(t)

	// No PutNar: neither a whole file nor chunks exist, and no chunk store is
	// configured. hasInStore=false reflects the genuine absence here.
	nu := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, _, err := c.serveNarFromStorageViaPipe(ctx, &nu, false)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a genuinely absent NAR with no chunk store must return not-found, not a chunk-store error")
	require.NotContains(t, err.Error(), "chunk store not initialized",
		"the serve path must not route to chunks when no chunk store is configured")
}
