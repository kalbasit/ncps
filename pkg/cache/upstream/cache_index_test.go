package upstream_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nixcacheindex"
	"github.com/kalbasit/ncps/testhelper"
)

func TestExperimentalCacheIndex(t *testing.T) {
	t.Parallel()

	// 1. Setup Mock Server
	var requestedNarInfo bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/nix-cache-info":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 40\n"))

		case r.URL.Path == "/nix-cache-index/manifest.json":
			w.WriteHeader(http.StatusOK)

			m := nixcacheindex.NewManifest()
			// Update URLs to point to this mock server
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}

			baseURL := fmt.Sprintf("%s://%s/nix-cache-index/", scheme, r.Host)
			m.Urls.JournalBase = baseURL + "journal/"
			m.Urls.ShardsBase = baseURL + "shards/"
			m.Urls.DeltasBase = baseURL + "deltas/"

			_ = json.NewEncoder(w).Encode(m)

		case strings.HasPrefix(r.URL.Path, "/nix-cache-index/journal/"):
			// Simulate missing/empty journal segments
			w.WriteHeader(http.StatusNotFound)

		case strings.HasPrefix(r.URL.Path, "/nix-cache-index/shards/"):
			// Simulate missing shards -> implies DefiniteMiss
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(r.URL.Path, ".narinfo"):
			requestedNarInfo = true

			w.WriteHeader(http.StatusOK)
			// Should not be reached in Hit case if we were testing Hits,
			// but for Miss check we want to ensure it's NOT reached
			_, _ = w.Write([]byte("StorePath: /nix/store/abc-example"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	// 2. Setup Upstream Cache with Index Enabled
	opts := &upstream.Options{
		ExperimentalCacheIndex: true,
	}

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		opts,
	)
	require.NoError(t, err)

	// 3. Perform Request
	// The mock setup ensures that checking shards returns 404, which the client interprets as DefiniteMiss.
	// Therefore, GetNarInfo should return ErrNotFound WITHOUT requesting the .narinfo file.

	_, err = c.GetNarInfo(context.Background(), "00000000000000000000000000000000") // 32 chars

	// 4. Verification
	require.ErrorIs(t, err, upstream.ErrNotFound)
	assert.False(t, requestedNarInfo, "Should not have requested the narinfo file from upstream")
}
