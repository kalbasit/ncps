package testdata_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/testdata"
)

func TestHTTPTestServer(t *testing.T) {
	t.Parallel()

	priority := 40

	ts := testdata.HTTPTestServer(t, priority)
	defer ts.Close()

	r, err := http.NewRequestWithContext(context.Background(), "GET", ts.URL+"/nix-cache-info", nil)
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

		if assert.NotEqual(t, testdata.NixStoreInfo(priority), string(body)) {
			decoder, err := zstd.NewReader(nil)
			require.NoError(t, err)

			plain, err := decoder.DecodeAll(body, []byte{})
			require.NoError(t, err)

			assert.Equal(t, testdata.NixStoreInfo(priority), string(plain))
		}
	}
}
