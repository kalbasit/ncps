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

var errGoAway = errors.New("http2: server sent GOAWAY and closed the connection; " +
	"LastStreamID=1045, ErrCode=PROTOCOL_ERROR, debug=\"\"")

type goAwayRoundTripper struct {
	count int
}

func (g *goAwayRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	g.count++
	if g.count == 1 {
		return nil, errGoAway
	}

	// Succeed on the second try
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)),
		Request:    req,
	}, nil
}

func TestGetNarInfo_RetryOnGoAway(t *testing.T) {
	t.Parallel()

	rt := &goAwayRoundTripper{}
	c, err := upstream.New(
		context.Background(),
		testhelper.MustParseURL(t, "https://cache.nixos.org"),
		&upstream.Options{
			Transport: rt,
		},
	)
	require.NoError(t, err)

	_, err = c.GetNarInfo(context.Background(), "hash")

	// This should succeed now as retries are implemented.
	require.NoError(t, err)
	assert.Equal(t, 2, rt.count)
}
