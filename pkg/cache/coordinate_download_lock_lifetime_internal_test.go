package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// downloadLockFree reports whether the per-hash NAR download lock is currently
// free. It probes with a non-blocking acquisition and immediately releases the
// probe hold if it succeeds, so it never leaves the lock held.
func downloadLockFree(c *Cache, key string) bool {
	acquired, err := c.downloadLocker.TryLock(context.Background(), key, c.downloadLockTTL)
	if err != nil {
		return false
	}

	if acquired {
		_ = c.downloadLocker.Unlock(context.Background(), key)

		return true
	}

	return false
}

// TestCoordinateDownload_HoldsNARLockThroughChunking verifies that for a NAR
// download (waitForStorage == false) the download lock is held until ds.done —
// i.e. through the eager-CDC chunking window — and is NOT released merely because
// ds.stored closed (which happens at chunk start, cache.go onNarFileReady).
//
// Releasing at ds.stored leaves the lock free while chunking runs in the
// background, letting a cross-pod reader acquire it mid-chunking and short-circuit
// to chunk-based serving, which 404s a compressed request (#1289). Holding the
// lock until ds.done forces such a reader to contend and engage in-flight staging
// instead.
func TestCoordinateDownload_HoldsNARLockThroughChunking(t *testing.T) {
	t.Parallel()

	c, _, _, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	ctx := context.Background()

	const hash = "0123456789abcdef0123456789abcdef"

	key := narJobKey(hash)

	// The asset is not present, so coordinateDownload starts the (stubbed) job
	// rather than short-circuiting to a served-from-storage state.
	checkAsset := func(context.Context) (bool, bool) { return false, false }

	// startJob signals the download has started but does not complete it; the test
	// drives ds.stored / ds.done explicitly to model the chunking window.
	startJob := func(ds *downloadState) {
		ds.startOnce.Do(func() { close(ds.start) })
	}

	ds := c.coordinateDownload(
		ctx, ctx, key, hash,
		false, // waitForStorage (NAR path)
		false, // allowStaging
		checkAsset,
		startJob,
	)

	// Always unblock the background release goroutine + refresher at test end so
	// c.Close() does not hang, even if an assertion fails early.
	t.Cleanup(func() {
		ds.storedOnce.Do(func() { close(ds.stored) })
		ds.doneOnce.Do(func() { close(ds.done) })
	})

	// In flight: the lock is held.
	assert.False(t, downloadLockFree(c, key),
		"download lock must be held while the download is in flight")

	// Chunk start: ds.stored closes. The lock must stay held through chunking.
	ds.storedOnce.Do(func() { close(ds.stored) })

	assert.Never(t, func() bool { return downloadLockFree(c, key) },
		300*time.Millisecond, 15*time.Millisecond,
		"download lock must stay held after ds.stored (through the eager-CDC chunking window)")

	// Chunk complete: ds.done closes. The lock is released in the background.
	ds.doneOnce.Do(func() { close(ds.done) })

	assert.Eventually(t, func() bool { return downloadLockFree(c, key) },
		2*time.Second, 15*time.Millisecond,
		"download lock must be released after ds.done (chunking complete)")
}
