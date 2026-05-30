package upstream_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// timestampRoundTripper records the wall-clock time of each call. It fails the
// first call with a retriable transport error, then succeeds with the Nar1
// narinfo fixture.
type timestampRoundTripper struct {
	mu    sync.Mutex
	times []time.Time
}

func (r *timestampRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.times = append(r.times, time.Now())
	n := len(r.times)
	r.mu.Unlock()

	if n == 1 {
		return nil, errConnReset
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)),
		Request:    req,
	}, nil
}

func TestDoRequest_RetriesUseBackoff(t *testing.T) {
	t.Parallel()

	const backoff = 40 * time.Millisecond

	rt := &timestampRoundTripper{}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{Transport: rt, RetryBackoff: backoff},
	)
	require.NoError(t, err)

	_, err = c.GetNarInfo(context.Background(), "hash")
	require.NoError(t, err)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	require.Len(t, rt.times, 2, "expected exactly one retry")

	gap := rt.times[1].Sub(rt.times[0])
	assert.GreaterOrEqual(t, gap, backoff*3/4,
		"a retry must wait a backoff delay (got %s), not retry immediately", gap)
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
