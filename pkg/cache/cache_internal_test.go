package cache

import (
	"math/rand/v2"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

func TestNew(t *testing.T) {
	t.Run("upstream caches", func(t *testing.T) {
		t.Parallel()

		testServers := make(map[int]*httptest.Server)

		for i := 1; i < 10; i++ {
			ts := testdata.HTTPTestServer(t, i)
			defer ts.Close()
			testServers[i] = ts
		}

		randomOrder := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
		rand.Shuffle(len(randomOrder), func(i, j int) { randomOrder[i], randomOrder[j] = randomOrder[j], randomOrder[i] })

		t.Logf("random order established: %v", randomOrder)

		ucs := make([]upstream.Cache, 0, len(testServers))

		for _, idx := range randomOrder {
			ts := testServers[idx]

			u, err := url.Parse(ts.URL)
			require.NoError(t, err)

			uc, err := upstream.New(logger, u.Host, nil)
			require.NoError(t, err)

			ucs = append(ucs, uc)
		}

		cachePath := os.TempDir()

		c, err := New(logger, "cache.example.com", cachePath, ucs)
		require.NoError(t, err)

		for idx, uc := range c.upstreamCaches {
			//nolint:gosec
			assert.EqualValues(t, idx+1, uc.GetPriority())
		}
	})
}
