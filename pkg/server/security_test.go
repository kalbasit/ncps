package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

//nolint:paralleltest
func TestSecurity(t *testing.T) {
	// Setup a dummy upstream server that records the paths it receives
	receivedPaths := make(chan string, 100)

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPaths <- r.URL.Path

		if r.URL.Path == "/nix-cache-info" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 40"))

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstreamServer.Close()

	// Setup ncps server
	dir, err := os.MkdirTemp("", "ncps-security-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)
	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	ls, err := local.New(context.Background(), dir)
	require.NoError(t, err)

	c, err := cache.New(context.Background(), "localhost", db, ls, ls, ls, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), time.Minute, time.Minute)
	require.NoError(t, err)

	uc, err := upstream.New(context.Background(), testhelper.MustParseURL(t, upstreamServer.URL), nil)
	require.NoError(t, err)
	c.AddUpstreamCaches(context.Background(), uc)

	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	// Drain receivedPaths (health check requests)
L:
	for {
		select {
		case <-receivedPaths:
		default:
			break L
		}
	}

	s := server.New(c)

	ts := httptest.NewServer(s)
	defer ts.Close()

	client := ts.Client()

	tests := []struct {
		name                string
		method              string
		path                string
		expectedStatus      int
		shouldReachUpstream bool
	}{
		{
			name:                "Valid 32-char narinfo hash",
			method:              http.MethodGet,
			path:                "/n5glp21rsz314qssw9fbvfswgy3kc68f.narinfo",
			expectedStatus:      http.StatusNotFound, // Not found upstream, but reached it
			shouldReachUpstream: true,
		},
		{
			name:                "Invalid hash length (31 chars)",
			method:              http.MethodGet,
			path:                "/n5glp21rsz314qssw9fbvfswgy3kc68.narinfo",
			expectedStatus:      http.StatusNotFound, // Doesn't match Chi route
			shouldReachUpstream: false,
		},
		{
			name:                "Invalid hash characters (upper case)",
			method:              http.MethodGet,
			path:                "/N5GLP21RSZ314QSSW9FBVFSWGY3KC68F.narinfo",
			expectedStatus:      http.StatusNotFound, // Doesn't match Chi route
			shouldReachUpstream: false,
		},
		{
			name:                "Path traversal attempt (alphanumeric but malicious)",
			method:              http.MethodGet,
			path:                "/aeou456789abcdfghijklmnpqrsvwxy.narinfo", // contains all 4 chars not allowed aeou
			expectedStatus:      http.StatusNotFound,
			shouldReachUpstream: false,
		},
		{
			name:                "Valid NAR hash (32 chars)",
			method:              http.MethodGet,
			path:                "/nar/1lid9xrpirkzcpqsxfq02qwiq0yd70ch.nar.xz",
			expectedStatus:      http.StatusNotFound,
			shouldReachUpstream: true,
		},
		{
			name:                "Valid NAR hash (52 chars)",
			method:              http.MethodGet,
			path:                "/nar/1lid9xrpirkzcpqsxfq02qwiq0yd70chfl860wzsqd1739ih0nri.nar.xz",
			expectedStatus:      http.StatusNotFound,
			shouldReachUpstream: true,
		},
	}

	for _, tt := range tests {
		//nolint:paralleltest
		t.Run(tt.name, func(t *testing.T) {
			// Clear channel
		L2:
			for {
				select {
				case path := <-receivedPaths:
					t.Logf("Drained path: %s", path)
				default:
					break L2
				}
			}

			req, _ := http.NewRequestWithContext(context.Background(), tt.method, ts.URL+tt.path, nil)
			resp, err := client.Do(req)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.shouldReachUpstream {
				select {
				case path := <-receivedPaths:
					t.Logf("Upstream received expected path: %s", path)
					// OK
				case <-time.After(500 * time.Millisecond):
					t.Error("Request should have reached upstream but didn't")
				}
			} else {
				select {
				case path := <-receivedPaths:
					t.Errorf("Request should NOT have reached upstream but reached it at path: %s", path)
				case <-time.After(500 * time.Millisecond):
					// OK
				}
			}
		})
	}
}
