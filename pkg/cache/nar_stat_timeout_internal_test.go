package cache

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
)

// slowStatStore wraps a NarStore and delays StatNar, modelling a storage backend
// whose presence probe is slow.
//
// ignoreCtx models the local backend: os.Stat bottoms out in fstatat(2), which
// takes no context and cannot be aborted from userspace, so the delay is served
// with an uninterruptible sleep. With ignoreCtx false it models the S3 backend,
// whose StatObject does observe context cancellation.
type slowStatStore struct {
	storage.NarStore

	delay     time.Duration
	ignoreCtx bool
	statCalls atomic.Int64
}

func (s *slowStatStore) StatNar(ctx context.Context, narURL nar.URL) (bool, error) {
	s.statCalls.Add(1)

	if s.ignoreCtx {
		time.Sleep(s.delay)

		return s.NarStore.StatNar(ctx, narURL)
	}

	select {
	case <-time.After(s.delay):
		return s.NarStore.StatNar(ctx, narURL)
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *slowStatStore) HasNar(ctx context.Context, narURL nar.URL) bool {
	present, _ := s.StatNar(ctx, narURL)

	return present
}

// TestSlowStatStoreBlocksAsConfigured verifies the test double itself (task 1.1):
// it must actually block for the configured delay, and must honour context
// cancellation only when it is not modelling the uncancellable local backend.
func TestSlowStatStoreBlocksAsConfigured(t *testing.T) {
	t.Parallel()

	t.Run("blocks for the configured delay", func(t *testing.T) {
		t.Parallel()

		c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
			return &slowStatStore{NarStore: s, delay: 200 * time.Millisecond}
		})

		store, ok := c.narStore.(*slowStatStore)
		require.True(t, ok)

		start := time.Now()
		_, err := store.StatNar(newContext(), nar.URL{Hash: testdata.Nar1.NarHash})
		require.NoError(t, err)

		assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
		assert.Equal(t, int64(1), store.statCalls.Load())
	})

	t.Run("context-aware double unblocks on cancellation", func(t *testing.T) {
		t.Parallel()

		c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
			return &slowStatStore{NarStore: s, delay: 30 * time.Second}
		})

		store, ok := c.narStore.(*slowStatStore)
		require.True(t, ok)

		ctx, cancel := context.WithTimeout(newContext(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := store.StatNar(ctx, nar.URL{Hash: testdata.Nar1.NarHash})

		require.Error(t, err)
		assert.Less(t, time.Since(start), 5*time.Second)
	})

	t.Run("uncancellable double ignores cancellation", func(t *testing.T) {
		t.Parallel()

		c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
			return &slowStatStore{NarStore: s, delay: 300 * time.Millisecond, ignoreCtx: true}
		})

		store, ok := c.narStore.(*slowStatStore)
		require.True(t, ok)

		ctx, cancel := context.WithTimeout(newContext(), 10*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, _ = store.StatNar(ctx, nar.URL{Hash: testdata.Nar1.NarHash})

		// The sleep is not interruptible, so it runs to completion despite the
		// cancelled context. This is the property that makes the local backend
		// impossible to cancel and forces the request to abandon it instead.
		assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	})
}

// TestGetNarBoundedTimeToFirstByte is the regression test for the production
// stall: GetNar MUST resolve within the configured stat timeout even when the
// storage presence probe blocks for far longer and cannot be cancelled.
//
// Evidence this reproduces: a goroutine dump taken against ncps v0.10.0-rc17
// caught a request parked in a single uncancellable fstatat(2) for ~57s inside
// statNarInStore, while the ingress aborted the stream at its 60s read timeout
// and delivered the client a 200 with a truncated body.
func TestGetNarBoundedTimeToFirstByte(t *testing.T) {
	t.Parallel()

	const (
		statTimeout = 250 * time.Millisecond
		probeDelay  = 30 * time.Second
		budget      = 10 * time.Second
	)

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: probeDelay, ignoreCtx: true}
	})

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	// Put the NAR through the underlying store directly so the write is not
	// itself delayed by the slow probe.
	inner, ok := c.narStore.(*slowStatStore)
	require.True(t, ok)

	_, err := inner.PutNar(
		newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)), -1,
	)
	require.NoError(t, err)

	c.SetStatTimeout(statTimeout)

	done := make(chan struct{})

	var (
		getErr error
		rc     io.ReadCloser
	)

	start := time.Now()

	go func() {
		defer close(done)

		_, _, rc, getErr = c.GetNar(newContext(), narURL)
	}()

	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("GetNar did not resolve within %s while the storage probe blocked for %s: "+
			"a NAR request must have a bounded time-to-first-byte", budget, probeDelay)
	}

	if rc != nil {
		defer rc.Close()
	}

	elapsed := time.Since(start)
	t.Logf("GetNar resolved in %s (probe delay %s, stat timeout %s, err=%v)",
		elapsed, probeDelay, statTimeout, getErr)

	assert.Less(t, elapsed, budget,
		"GetNar must resolve well inside a reverse proxy read timeout")

	// Either bytes or a clean error is acceptable; a silent hang is not.
	if getErr == nil {
		require.NotNil(t, rc)
	}
}

// TestStatNarInStoreTimeoutIsIndeterminate asserts a timed-out presence probe is
// classified as undeterminable — (false, err) — and never as a confirmed absence
// — (false, nil). Reporting a stalled probe as absence would silently convert a
// slow read into a redundant re-download or a spurious 404.
func TestStatNarInStoreTimeoutIsIndeterminate(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: 30 * time.Second, ignoreCtx: true}
	})
	c.SetStatTimeout(250 * time.Millisecond)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	type result struct {
		present bool
		err     error
	}

	ch := make(chan result, 1)

	go func() {
		present, err := c.statNarInStore(newContext(), narURL)
		ch <- result{present, err}
	}()

	select {
	case got := <-ch:
		assert.False(t, got.present, "a timed-out probe must not report presence")
		require.Error(t, got.err,
			"a timed-out probe must be undeterminable (false, err), never a confirmed absence (false, nil)")
		require.ErrorIs(t, got.err, ErrStatTimeout)
	case <-time.After(10 * time.Second):
		t.Fatal("statNarInStore did not return within 10s; the probe bound is not applied")
	}
}

// TestStatNarInStoreFastProbeUnaffected pins the no-regression half: a healthy
// store must behave exactly as before, with no timeout error and no added latency.
func TestStatNarInStoreFastProbeUnaffected(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore { return s })
	c.SetStatTimeout(5 * time.Second)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	present, err := c.statNarInStore(newContext(), narURL)
	require.NoError(t, err)
	assert.False(t, present, "nothing was stored yet: this is a confirmed absence")

	require.NoError(t, c.PutNar(newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText))))

	present, err = c.statNarInStore(newContext(), narURL)
	require.NoError(t, err)
	assert.True(t, present)
}

// TestUploadOnlyIndeterminateIsNotNotFound is the guard against re-introducing the
// phantom-NAR bug (design.md D3).
//
// In upload-only mode GetNar returns storage.ErrNotFound to tell the client "we do
// not have it, please PUT it". If a *stalled* probe took that branch, `nix copy`
// would skip the NAR upload and leave a phantom whose later reference check 404s.
// An undeterminable probe must therefore surface a retryable error instead.
func TestUploadOnlyIndeterminateIsNotNotFound(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: 30 * time.Second, ignoreCtx: true}
	})
	c.SetStatTimeout(250 * time.Millisecond)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	// Store the bytes underneath the slow probe: the NAR really is present, so
	// reporting "not found" here would be actively wrong as well as harmful.
	inner, ok := c.narStore.(*slowStatStore)
	require.True(t, ok)

	_, err := inner.PutNar(
		newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)), -1,
	)
	require.NoError(t, err)

	ch := make(chan error, 1)

	go func() {
		_, _, rc, getErr := c.GetNar(WithUploadOnly(newContext()), narURL)
		if rc != nil {
			_ = rc.Close()
		}

		ch <- getErr
	}()

	select {
	case getErr := <-ch:
		require.NotErrorIs(t, getErr, storage.ErrNotFound,
			"an undeterminable probe must not be reported as ErrNotFound in upload-only mode: "+
				"the client would skip the upload and leave a phantom NAR")
	case <-time.After(10 * time.Second):
		t.Fatal("GetNar did not resolve within 10s in upload-only mode")
	}
}

// TestStatProbeIsSingleFlighted asserts concurrent requests for the same NAR
// collapse onto one backend probe (design D2). Without this, a stalled NAR under
// a client retry storm costs one blocked goroutine — and on the local backend one
// pinned OS thread — per client.
func TestStatProbeIsSingleFlighted(t *testing.T) {
	t.Parallel()

	const callers = 20

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: 2 * time.Second, ignoreCtx: true}
	})
	c.SetStatTimeout(200 * time.Millisecond)

	store, ok := c.narStore.(*slowStatStore)
	require.True(t, ok)

	// A single explicit compression means statNarInStore probes exactly one key.
	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

	var wg sync.WaitGroup

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = c.statNarInStore(newContext(), narURL)
		}()
	}

	wg.Wait()

	got := store.statCalls.Load()
	t.Logf("%d concurrent callers produced %d backend probe(s)", callers, got)

	assert.Less(t, got, int64(callers),
		"concurrent probes for the same NAR must be collapsed, not issued per caller")
	assert.LessOrEqual(t, got, int64(2),
		"a single in-flight window should produce at most one shared probe")
}

// ctxRecordingStore records what context the backend actually received, so the
// deadline-propagation contract can be asserted directly rather than inferred.
type ctxRecordingStore struct {
	storage.NarStore

	delay       time.Duration
	sawDeadline atomic.Bool
	sawCancel   atomic.Bool
	done        chan struct{}
}

func (s *ctxRecordingStore) StatNar(ctx context.Context, narURL nar.URL) (bool, error) {
	if _, ok := ctx.Deadline(); ok {
		s.sawDeadline.Store(true)
	}

	defer close(s.done)

	select {
	case <-time.After(s.delay):
		return s.NarStore.StatNar(ctx, narURL)
	case <-ctx.Done():
		s.sawCancel.Store(true)

		return false, ctx.Err()
	}
}

// TestStatProbeDeadlineReachesBackend asserts the probe bound is propagated into
// the context handed to the storage backend, so a context-aware backend (S3,
// whose StatObject takes a context) genuinely cancels its request instead of
// merely being abandoned.
//
// This is asserted against a recording double rather than a live S3 bucket on
// purpose: the property under test is that *ncps* supplies a cancellable,
// deadline-carrying context. Whether MinIO/Garage honours a cancelled context is
// their contract, not this codebase's, and asserting it would need live
// infrastructure to test something we do not control.
func TestStatProbeDeadlineReachesBackend(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &ctxRecordingStore{NarStore: s, delay: 30 * time.Second, done: make(chan struct{})}
	})
	c.SetStatTimeout(200 * time.Millisecond)

	store, ok := c.narStore.(*ctxRecordingStore)
	require.True(t, ok)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

	_, err := c.statNarInStore(newContext(), narURL)
	require.ErrorIs(t, err, ErrStatTimeout)

	// The probe goroutine outlives the caller; wait for it to observe cancellation.
	select {
	case <-store.done:
	case <-time.After(10 * time.Second):
		t.Fatal("backend probe never returned after the deadline expired")
	}

	assert.True(t, store.sawDeadline.Load(),
		"the backend must receive a context carrying the probe deadline")
	assert.True(t, store.sawCancel.Load(),
		"a context-aware backend must be cancelled at the deadline, not merely abandoned")
}

// TestStalledProbeIsLogged asserts a stalled probe is not silent.
//
// Silence is itself the defect this change addresses: in production a request
// spent ~57s inside one storage probe and emitted no log line, span or metric
// for the entire stall, which is why the cause took so long to find. A healthy
// probe must stay quiet, so the signal means something.
func TestStalledProbeIsLogged(t *testing.T) {
	t.Parallel()

	newLoggingCtx := func(buf *bytes.Buffer) context.Context {
		return zerolog.New(buf).With().Logger().WithContext(context.Background())
	}

	t.Run("timed-out probe emits a warning naming the NAR and elapsed time", func(t *testing.T) {
		t.Parallel()

		c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
			return &slowStatStore{NarStore: s, delay: 30 * time.Second, ignoreCtx: true}
		})
		c.SetStatTimeout(200 * time.Millisecond)

		var buf bytes.Buffer

		narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

		_, err := c.statNarInStore(newLoggingCtx(&buf), narURL)
		require.ErrorIs(t, err, ErrStatTimeout)

		logged := buf.String()
		assert.Contains(t, logged, "storage presence probe timed out")
		assert.Contains(t, logged, testdata.Nar1.NarHash, "the log must name the NAR")
		assert.Contains(t, logged, `"elapsed"`, "the log must report how long it waited")
		assert.Contains(t, logged, `"warn"`)
	})

	t.Run("fast probe stays silent", func(t *testing.T) {
		t.Parallel()

		c := newServableTestCache(t, func(s *local.Store) storage.NarStore { return s })
		c.SetStatTimeout(5 * time.Second)

		var buf bytes.Buffer

		narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}

		_, err := c.statNarInStore(newLoggingCtx(&buf), narURL)
		require.NoError(t, err)

		assert.NotContains(t, buf.String(), "storage presence probe timed out",
			"a healthy probe must not emit a timeout warning")
	})
}

// TestTimeoutIsNotReportedAsNotFound covers the non-upload-only half of the
// "a timed-out probe is not an absence" requirement: the ordinary read path must
// not turn a stalled probe into a 404 either. The server maps storage.ErrNotFound
// to 404, so surfacing it here would tell a client the NAR does not exist when in
// fact it was never checked.
func TestTimeoutIsNotReportedAsNotFound(t *testing.T) {
	t.Parallel()

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: 30 * time.Second, ignoreCtx: true}
	})
	c.SetStatTimeout(250 * time.Millisecond)

	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: testdata.Nar1.NarCompression}

	inner, ok := c.narStore.(*slowStatStore)
	require.True(t, ok)

	_, err := inner.PutNar(
		newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)), -1,
	)
	require.NoError(t, err)

	ch := make(chan error, 1)

	go func() {
		_, _, rc, getErr := c.GetNar(newContext(), narURL)
		if rc != nil {
			_ = rc.Close()
		}

		ch <- getErr
	}()

	select {
	case getErr := <-ch:
		require.NotErrorIs(t, getErr, storage.ErrNotFound,
			"a stalled probe must never surface as a 404: the NAR was not checked, not absent")
	case <-time.After(10 * time.Second):
		t.Fatal("GetNar did not resolve within 10s")
	}
}

// TestRequestProbeBudgetIsCumulative pins the request-level bound.
//
// Bounding each probe individually is not sufficient. A single GetNar consults the
// store several times — the pre-check, the servability lookup, and again after
// download coordination — so N stalled probes cost N x statTimeout unless they
// share one budget. Measured at 4.0x the configured bound before the budget
// existed; at a 15s setting that would put a request back over a 60s proxy read
// timeout, reintroducing the exact production failure this change fixes.
func TestRequestProbeBudgetIsCumulative(t *testing.T) {
	t.Parallel()

	const statTimeout = 300 * time.Millisecond

	c := newServableTestCache(t, func(s *local.Store) storage.NarStore {
		return &slowStatStore{NarStore: s, delay: 60 * time.Second, ignoreCtx: true}
	})
	c.SetStatTimeout(statTimeout)

	inner, ok := c.narStore.(*slowStatStore)
	require.True(t, ok)

	// Compression:none makes statNarInStore consider several candidate URLs, and
	// GetNar consults the store more than once, so this is the worst case.
	narURL := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeNone}

	_, err := inner.PutNar(
		newContext(), narURL, io.NopCloser(strings.NewReader(testdata.Nar1.NarText)), -1,
	)
	require.NoError(t, err)

	done := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(done)

		_, _, rc, _ := c.GetNar(newContext(), narURL)
		if rc != nil {
			_ = rc.Close()
		}
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("GetNar never returned")
	}

	elapsed := time.Since(start)
	t.Logf("GetNar spent %v against a %v cumulative budget (%.1fx)",
		elapsed, statTimeout, float64(elapsed)/float64(statTimeout))

	// Generous slack for scheduling, but far below the 2x that would prove the
	// budget is per-probe rather than per-request.
	assert.Less(t, elapsed, 2*statTimeout,
		"a request must not accumulate multiple full probe timeouts")
}
