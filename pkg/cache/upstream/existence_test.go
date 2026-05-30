package upstream_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

var errNoSuchHost = errors.New("dial tcp: lookup cache.example: no such host")

// alwaysErrRoundTripper always fails with a non-retriable transport error.
type alwaysErrRoundTripper struct{}

func (alwaysErrRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errNoSuchHost
}

func TestNarInfoExistence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rt   http.RoundTripper
		want upstream.Existence
	}{
		{
			name: "200 means present",
			rt:   &statusRoundTripper{status: http.StatusOK},
			want: upstream.ExistencePresent,
		},
		{
			name: "404 means definitely absent",
			rt:   &statusRoundTripper{status: http.StatusNotFound},
			want: upstream.ExistenceAbsent,
		},
		{
			name: "transport error means unknown",
			rt:   alwaysErrRoundTripper{},
			want: upstream.ExistenceUnknown,
		},
		{
			name: "5xx means unknown, not absent",
			rt:   &statusRoundTripper{status: http.StatusServiceUnavailable},
			want: upstream.ExistenceUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := upstream.New(
				context.Background(),
				testhelper.MustParseURL(t, "https://cache.nixos.org"),
				&upstream.Options{Transport: tc.rt},
			)
			require.NoError(t, err)

			assert.Equal(t, tc.want, c.NarInfoExistence(context.Background(), testdata.Nar1.NarInfoHash))
		})
	}
}
