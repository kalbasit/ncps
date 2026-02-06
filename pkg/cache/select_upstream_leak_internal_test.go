package cache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
)

var errUpstreamUnavailable = errors.New("upstream unavailable")

// createDummyUpstreams creates n dummy upstream caches backed by test HTTP servers.
// The servers are closed when the test finishes.
func createDummyUpstreams(t *testing.T, n int) []*upstream.Cache {
	t.Helper()

	ucs := make([]*upstream.Cache, n)

	for i := range ucs {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(ts.Close)

		u, err := url.Parse(ts.URL)
		require.NoError(t, err)

		uc, err := upstream.New(newContext(), u, nil)
		require.NoError(t, err)

		ucs[i] = uc
	}

	return ucs
}

func TestSelectUpstream_NoGoroutineLeak(t *testing.T) {
	t.Parallel()

	t.Run("all upstreams succeed should not leak goroutines", func(t *testing.T) {
		t.Parallel()

		const numUpstreams = 3

		ucs := createDummyUpstreams(t, numUpstreams)

		c := &Cache{}

		var completed int32

		allReady := make(chan struct{})

		var readyCount int32

		selectFn := func(
			_ context.Context,
			uc *upstream.Cache,
			wg *sync.WaitGroup,
			ch chan *upstream.Cache,
			_ chan error,
		) {
			defer wg.Done()
			defer atomic.AddInt32(&completed, 1)

			// Ensure all goroutines are running before any sends
			if atomic.AddInt32(&readyCount, 1) == numUpstreams {
				close(allReady)
			}

			<-allReady

			// All goroutines try to send simultaneously.
			// Only one send will be consumed by selectUpstream's for loop.
			// The rest will block forever on the unbuffered channel (goroutine leak).
			ch <- uc
		}

		result, err := c.selectUpstream(newContext(), ucs, selectFn)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Give goroutines time to complete (they should if not leaked)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(
			t,
			int32(numUpstreams),
			atomic.LoadInt32(&completed),
			"all worker goroutines should complete without leaking",
		)
	})

	t.Run("one succeeds while others error should not leak goroutines", func(t *testing.T) {
		t.Parallel()

		const numUpstreams = 3

		ucs := createDummyUpstreams(t, numUpstreams)

		c := &Cache{}

		var completed int32

		selectFn := func(
			_ context.Context,
			uc *upstream.Cache,
			wg *sync.WaitGroup,
			ch chan *upstream.Cache,
			errC chan error,
		) {
			defer wg.Done()
			defer atomic.AddInt32(&completed, 1)

			if uc == ucs[0] {
				// First upstream succeeds immediately
				ch <- uc
			} else {
				// Error workers delay to ensure the main loop has already
				// consumed from ch and returned. After the function returns,
				// nobody reads from errC, so these sends block forever.
				time.Sleep(100 * time.Millisecond)

				errC <- errUpstreamUnavailable
			}
		}

		result, err := c.selectUpstream(newContext(), ucs, selectFn)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Give goroutines time to complete (they should if not leaked)
		time.Sleep(500 * time.Millisecond)

		assert.Equal(
			t,
			int32(numUpstreams),
			atomic.LoadInt32(&completed),
			"all worker goroutines should complete without leaking",
		)
	})
}
