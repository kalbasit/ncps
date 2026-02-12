package testdata_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/zstd"
	"github.com/kalbasit/ncps/testdata"
)

func TestNewTestServer(t *testing.T) {
	t.Parallel()

	priority := 40

	ts := testdata.NewTestServer(t, priority)
	t.Cleanup(ts.Close)

	u := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if assert.Equal(t, http.StatusOK, resp.StatusCode) {
		assert.NotEqual(t, "zstd", resp.Header.Get("Content-Encoding"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Len(t, testdata.Nar1.NarText, len(string(body)))
		assert.Equal(t, testdata.Nar1.NarText, string(body))
	}
}

func TestNewTestServerWithZSTD(t *testing.T) {
	t.Parallel()

	priority := 40

	ts := testdata.NewTestServer(t, priority)
	t.Cleanup(ts.Close)

	u := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar"

	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	require.NoError(t, err)

	r.Header.Set("Accept-Encoding", "zstd")

	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if assert.Equal(t, http.StatusOK, resp.StatusCode) {
		assert.Equal(t, "zstd", resp.Header.Get("Content-Encoding"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		if assert.NotEqual(t, testdata.Nar1.NarText, string(body)) {
			dec := zstd.GetReader()
			defer zstd.PutReader(dec)

			plain, err := dec.DecodeAll(body, []byte{})
			require.NoError(t, err)

			if assert.Len(t, testdata.Nar1.NarText, len(string(plain))) {
				assert.Equal(t, testdata.Nar1.NarText, string(plain))
			}
		}
	}
}
