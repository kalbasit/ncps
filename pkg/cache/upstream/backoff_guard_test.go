package upstream_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testhelper"
)

// alwaysTransientRoundTripper always fails with a retriable transport error,
// counting attempts.
type alwaysTransientRoundTripper struct {
	count int
}

func (r *alwaysTransientRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.count++

	return nil, errConnReset
}

// delayRecorder records each requested retry backoff delay instead of sleeping,
// so backoff assertions do not depend on wall-clock time or scheduler load.
type delayRecorder struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (r *delayRecorder) wait(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.delays = append(r.delays, d)

	return nil
}

func (r *delayRecorder) recorded() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]time.Duration(nil), r.delays...)
}

// TestDoRequest_NoBackoffOnFinalAttempt verifies that the backoff wait is not
// applied after the last attempt, which would just add latency to an
// already-doomed request without enabling another retry.
func TestDoRequest_NoBackoffOnFinalAttempt(t *testing.T) {
	t.Parallel()

	const base = 60 * time.Millisecond

	rt := &alwaysTransientRoundTripper{}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{Transport: rt, RetryBackoff: base},
	)
	require.NoError(t, err)

	rec := &delayRecorder{}
	c.SetRetryWaitForTest(rec.wait)

	_, err = c.GetNarInfo(context.Background(), "hash")
	require.Error(t, err, "an always-failing transient request must ultimately error")

	// All attempts must run (3 == defaultHTTPRetries): this guards against a
	// regression that skips retries entirely.
	assert.Equal(t, 3, rt.count, "every retry attempt must be made")

	// Backoffs are base*2^i for i = 0, 1 (after attempts 1 and 2) but NOT after the
	// final attempt (i = 2).
	assert.Equal(t, []time.Duration{base, 2 * base}, rec.recorded(),
		"backoff must double between attempts and must not be applied after the final attempt")
}
