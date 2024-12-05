package cache

import (
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/inconshreveable/log15/v3"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
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

		handlerFunc := func(priority int) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/nix-cache-info" {
					w.WriteHeader(http.StatusNotFound)

					return
				}

				body := `StoreDir: /nix/store
WantMassQuery: 1
Priority: ` + strconv.Itoa(priority)

				if _, err := w.Write([]byte(body)); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}
			})
		}

		testServers := make(map[int]*httptest.Server)

		for i := 1; i < 10; i++ {
			ts := httptest.NewServer(handlerFunc(i))
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

		c, err := New(logger, "cache.example.com", cachePath, ucs)
		if err != nil {
			t.Fatalf("error creating a new cache: %s", err)
		}

		for idx, uc := range c.upstreamCaches {
			//nolint:gosec
			if want, got := uint64(idx+1), uc.GetPriority(); want != got {
				t.Errorf("expected the priority at index %d to be %d but got %d", idx, want, got)
			}
		}
	})
}
