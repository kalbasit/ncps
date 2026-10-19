package upstream_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestDoRequest_RetriesUseBackoff(t *testing.T) {
	t.Parallel()

	const backoff = 40 * time.Millisecond

	rt := &failOnceRoundTripper{err: errConnReset}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{Transport: rt, RetryBackoff: backoff},
	)
	require.NoError(t, err)

	rec := &delayRecorder{}
	c.SetRetryWaitForTest(rec.wait)

	_, err = c.GetNarInfo(context.Background(), "hash")
	require.NoError(t, err)

	assert.Equal(t, 2, rt.count, "expected exactly one retry")
	assert.Equal(t, []time.Duration{backoff}, rec.recorded(),
		"a retry must wait the base backoff delay, not retry immediately")
}

// TestDoRequest_BackoffDelayIsCapped verifies that the per-attempt backoff never
// exceeds the fixed cap, even when the configured base is larger than it.
func TestDoRequest_BackoffDelayIsCapped(t *testing.T) {
	t.Parallel()

	// Matches defaultRetryBackoffCap in cache.go.
	const backoffCap = 2 * time.Second

	rt := &alwaysTransientRoundTripper{}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{Transport: rt, RetryBackoff: 5 * time.Second},
	)
	require.NoError(t, err)

	rec := &delayRecorder{}
	c.SetRetryWaitForTest(rec.wait)

	_, err = c.GetNarInfo(context.Background(), "hash")
	require.Error(t, err)

	assert.Equal(t, []time.Duration{backoffCap, backoffCap}, rec.recorded(),
		"every backoff delay must be clamped to the cap")
}

// cancelOnFailRoundTripper cancels the request context and returns a retriable
// error on its first call, so doRequest enters its backoff wait with an
// already-cancelled context.
type cancelOnFailRoundTripper struct {
	cancel context.CancelFunc
	count  int
}

func (r *cancelOnFailRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.count++
	if r.count == 1 {
		r.cancel()

		return nil, errConnReset
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)),
		Request:    req,
	}, nil
}

func TestDoRequest_BackoffRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt := &cancelOnFailRoundTripper{cancel: cancel}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		// A long backoff would make this test hang if cancellation were ignored.
		&upstream.Options{Transport: rt, RetryBackoff: 30 * time.Second},
	)
	require.NoError(t, err)

	start := time.Now()
	_, err = c.GetNarInfo(ctx, "hash")
	elapsed := time.Since(start)

	require.Error(t, err, "a cancelled context during backoff must surface an error")
	assert.Less(t, elapsed, 5*time.Second,
		"cancellation must abort the backoff wait promptly (took %s)", elapsed)
	assert.Equal(t, 1, rt.count, "no second attempt after the context is cancelled")
}
