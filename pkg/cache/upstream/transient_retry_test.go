package upstream_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

var (
	errHTTP2Timeout = errors.New("http2: timeout awaiting response headers")
	errConnReset    = errors.New(
		"read tcp 10.0.0.1:443->10.0.0.2:443: read: connection reset by peer",
	)
	errBrokenPipe = errors.New(
		"write tcp 10.0.0.1:443->10.0.0.2:443: write: broken pipe",
	)
)

// failOnceRoundTripper fails the first request with the configured error, then
// succeeds, serving the Nar1 narinfo fixture so signature validation passes.
type failOnceRoundTripper struct {
	err   error
	count int
}

func (f *failOnceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	f.count++
	if f.count == 1 {
		return nil, f.err
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)),
		Request:    req,
	}, nil
}

func TestDoRequest_RetryOnTransientErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "http2 timeout awaiting response headers", err: errHTTP2Timeout},
		{name: "connection reset by peer", err: errConnReset},
		{name: "broken pipe", err: errBrokenPipe},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt := &failOnceRoundTripper{err: tc.err}
			c, err := upstream.New(
				context.Background(),
				testhelper.MustParseURL(t, "https://cache.nixos.org"),
				&upstream.Options{Transport: rt},
			)
			require.NoError(t, err)

			_, err = c.GetNarInfo(context.Background(), "hash")
			require.NoError(t, err, "transient error should be retried and then succeed")
			assert.Equal(t, 2, rt.count, "request should have been retried exactly once")
		})
	}
}

// statusRoundTripper always returns a response with the configured status code
// and counts how many times it was invoked.
type statusRoundTripper struct {
	status int
	count  int
}

func (s *statusRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.count++

	return &http.Response{
		StatusCode: s.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestDoRequest_GenuineNotFoundIsNotRetried(t *testing.T) {
	t.Parallel()

	rt := &statusRoundTripper{status: http.StatusNotFound}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{Transport: rt},
	)
	require.NoError(t, err)

	_, err = c.GetNarInfo(context.Background(), "hash")
	require.Error(t, err, "a genuine 404 must surface as an error, not succeed")
	assert.Equal(t, 1, rt.count, "a genuine 404 must NOT be retried")
}
