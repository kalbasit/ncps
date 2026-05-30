package upstream_test

import (
	"context"
	"net/http"
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

	start := time.Now()
	_, err = c.GetNarInfo(context.Background(), "hash")
	elapsed := time.Since(start)

	require.Error(t, err, "an always-failing transient request must ultimately error")

	// All attempts must run (3 == defaultHTTPRetries): this guards against a
	// regression that skips retries entirely, which the timing check alone would miss.
	assert.Equal(t, 3, rt.count, "every retry attempt must be made")

	// Backoffs are base*2^i for i = 0, 1 (after attempts 1 and 2) but NOT after the
	// final attempt (i = 2). Total ~= base + 2*base = 3*base. With a needless final
	// backoff it would be ~3*base + 4*base = 7*base.
	assert.Less(t, elapsed, 5*base,
		"backoff must not be applied after the final attempt (elapsed %s)", elapsed)
}
