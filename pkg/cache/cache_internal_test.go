package cache

import (
	"math/rand/v2"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/inconshreveable/log15/v3"

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
	t.Run("upstream caches added at once", func(t *testing.T) {
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
			if err != nil {
				t.Fatalf("error parsing the test server url: %s", err)
			}

			uc, err := upstream.New(logger, u.Host, nil)
			if err != nil {
				t.Fatalf("error creating an upstream cache: %s", err)
			}

			ucs = append(ucs, uc)
		}

		cachePath := os.TempDir()

		c, err := New(logger, "cache.example.com", cachePath)
		if err != nil {
			t.Fatalf("error creating a new cache: %s", err)
		}

		c.AddUpstreamCaches(ucs...)

		for idx, uc := range c.upstreamCaches {
			//nolint:gosec
			if want, got := uint64(idx+1), uc.GetPriority(); want != got {
				t.Errorf("expected the priority at index %d to be %d but got %d", idx, want, got)
			}
		}
	})

	t.Run("upstream caches added one by one", func(t *testing.T) {
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
			if err != nil {
				t.Fatalf("error parsing the test server url: %s", err)
			}

			uc, err := upstream.New(logger, u.Host, nil)
			if err != nil {
				t.Fatalf("error creating an upstream cache: %s", err)
			}

			ucs = append(ucs, uc)
		}

		cachePath := os.TempDir()

		c, err := New(logger, "cache.example.com", cachePath)
		if err != nil {
			t.Fatalf("error creating a new cache: %s", err)
		}

		for _, uc := range ucs {
			c.AddUpstreamCaches(uc)
		}

		for idx, uc := range c.upstreamCaches {
			//nolint:gosec
			if want, got := uint64(idx+1), uc.GetPriority(); want != got {
				t.Errorf("expected the priority at index %d to be %d but got %d", idx, want, got)
			}
		}
	})
}
