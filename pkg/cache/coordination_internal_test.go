package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// takeoverLocker is a test lock.Locker that simulates a distributed lock held
// by another replica. While a key is "blocked", Lock returns an error
// ("lock already taken"), mirroring the Redis locker's behavior under
// contention. Once release() is called, Lock for that key succeeds again,
// modelling the holder finishing or failing and releasing the lock.
//
// Intra-process download de-duplication is provided by the cache's own
// upstreamJobs map, so this mock intentionally does NOT enforce mutual
// exclusion — it only controls when acquisition succeeds.
var errLockTaken = errors.New("lock already taken")

type takeoverLocker struct {
	mu      sync.Mutex
	blocked map[string]bool
}

func newTakeoverLocker() *takeoverLocker {
	return &takeoverLocker{blocked: make(map[string]bool)}
}

func (l *takeoverLocker) block(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.blocked[key] = true
}

func (l *takeoverLocker) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.blocked, key)
}

func (l *takeoverLocker) Lock(_ context.Context, key string, _ time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.blocked[key] {
		return fmt.Errorf("%w: %s", errLockTaken, key)
	}

	return nil
}

func (l *takeoverLocker) Unlock(_ context.Context, _ string) error { return nil }

func (l *takeoverLocker) TryLock(_ context.Context, key string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return !l.blocked[key], nil
}

func (l *takeoverLocker) Extend(_ context.Context, _ string) error { return nil }

// setupTakeoverCache builds a cache wired to a takeoverLocker as its download
// locker and a real upstream test server, with a short download lock TTL so the
// coordination give-up bound is reached quickly in tests.
func setupTakeoverCache(t *testing.T) (*Cache, *takeoverLocker) {
	t.Helper()

	dir, err := os.MkdirTemp("", "cache-coord-")
	require.NoError(t, err)

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	locker := newTakeoverLocker()
	cacheLocker := locklocal.NewRWLocker()

	// Short download lock TTL keeps the give-up bound small. The coordination
	// give-up bound is max(downloadLockTTL, downloadPollTimeout) = 2s here, so a
	// waiter that never gets to take over gives up (with a cache miss) quickly.
	c, err := New(newContext(), cacheName, dbClient, localStore, localStore, localStore, "",
		locker, cacheLocker, 2*time.Second, 2*time.Second, cacheLockTTL)
	require.NoError(t, err)

	ts := testdata.NewTestServer(t, 40)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	c.SetRecordAgeIgnoreTouch(0)

	// Wait for upstream caches to become available.
	<-c.GetHealthChecker().Trigger()

	t.Cleanup(func() {
		c.Close()
		_ = dbClient.Close()
		ts.Close()
		os.RemoveAll(dir)
	})

	return c, locker
}

// TestCoordinateDownloadTakesOverNAR asserts that when a replica fails to
// acquire the NAR download lock (held by another replica) and the holder then
// releases the lock without producing the asset, the waiter takes over the
// download and serves the NAR rather than returning an error (HTTP 500).
func TestCoordinateDownloadTakesOverNAR(t *testing.T) {
	t.Parallel()

	c, locker := setupTakeoverCache(t)

	narKey := narJobKey(testdata.Nar1.NarHash)

	// Simulate another replica holding the NAR download lock from the start, so
	// neither the narinfo-triggered background pre-pull nor the explicit GetNar
	// below can acquire it until we release.
	locker.block(narKey)

	// Populate the narinfo (its lock key is not blocked) so GetNar can resolve
	// the NAR.
	_, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	type result struct {
		body []byte
		err  error
	}

	resCh := make(chan result, 1)

	nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

	go func() {
		_, _, r, getErr := c.GetNar(newContext(), nu)
		if getErr != nil {
			resCh <- result{err: getErr}

			return
		}

		defer r.Close()

		body, readErr := io.ReadAll(r)
		resCh <- result{body: body, err: readErr}
	}()

	// Give GetNar time to fail the initial acquisition and enter the
	// poll-and-reacquire fallback, then let the "holder" release the lock.
	time.Sleep(500 * time.Millisecond)
	locker.release(narKey)

	select {
	case res := <-resCh:
		require.NoError(t, res.err,
			"a lock-losing waiter must take over the download and serve the NAR, not return an error (500)")
		assert.Equal(t, testdata.Nar1.NarText, string(res.body))
	case <-time.After(8 * time.Second):
		t.Fatal("GetNar did not complete after the download lock was released")
	}
}

// TestCoordinateDownloadNarInfoMissReturnsNotFound asserts that when a replica
// fails to acquire the narinfo download lock and the narinfo is genuinely
// absent upstream, the waiter resolves to a cache miss (storage.ErrNotFound,
// which the server maps to HTTP 404) rather than the coordination error that
// the server maps to HTTP 500.
func TestCoordinateDownloadNarInfoMissReturnsNotFound(t *testing.T) {
	t.Parallel()

	c, locker := setupTakeoverCache(t)

	const hash = "doesnotexist"

	niKey := narInfoJobKey(hash)
	locker.block(niKey)

	resCh := make(chan error, 1)

	go func() {
		_, err := c.GetNarInfo(newContext(), hash)
		resCh <- err
	}()

	time.Sleep(500 * time.Millisecond)
	locker.release(niKey)

	select {
	case err := <-resCh:
		require.Error(t, err)
		require.ErrorIs(t, err, storage.ErrNotFound,
			"a lock-losing waiter for a missing narinfo must return a cache miss (404), not a coordination error (500)")
	case <-time.After(8 * time.Second):
		t.Fatal("GetNarInfo did not complete after the download lock was released")
	}
}

// TestCoordinateDownloadNARGiveUpReturnsNotFound asserts that when a replica
// fails to acquire the NAR download lock and the holder neither produces the
// asset nor releases the lock within the give-up bound (a stuck or
// legitimately-slow holder still refreshing its lock), the waiter returns a
// cache miss (storage.ErrNotFound, which the server maps to HTTP 404) rather
// than the coordination error that the server maps to HTTP 500.
func TestCoordinateDownloadNARGiveUpReturnsNotFound(t *testing.T) {
	t.Parallel()

	c, locker := setupTakeoverCache(t)

	narKey := narJobKey(testdata.Nar1.NarHash)

	// Hold the NAR download lock for the entire test: the waiter can neither
	// observe the asset (it is never produced) nor re-acquire the lock, so it
	// must hit the give-up bound and degrade to a cache miss — never a 500.
	locker.block(narKey)

	// Populate the narinfo (its lock key is not blocked) so GetNar proceeds to
	// the NAR download coordination.
	_, err := c.GetNarInfo(newContext(), testdata.Nar1.NarInfoHash)
	require.NoError(t, err)

	resCh := make(chan error, 1)

	nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

	go func() {
		_, _, r, getErr := c.GetNar(newContext(), nu)
		if r != nil {
			r.Close()
		}

		resCh <- getErr
	}()

	select {
	case err := <-resCh:
		require.Error(t, err)
		require.ErrorIs(t, err, storage.ErrNotFound,
			"a lock-losing waiter that gives up must return a cache miss (404), not a coordination error (500)")
	case <-time.After(8 * time.Second):
		t.Fatal("GetNar did not complete within the give-up bound")
	}
}
