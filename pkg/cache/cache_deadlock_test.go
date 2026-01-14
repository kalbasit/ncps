package cache //nolint:testpackage

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// TestDeadlock_NarInfo_Triggers_Nar_Refetch reproduces a deadlock where pulling a NarInfo
// triggers a Nar fetch (because compression is none), and both waiting on each other
// if they share the same lock/job key.
func TestDeadlock_NarInfo_Triggers_Nar_Refetch(t *testing.T) {
	t.Parallel()

	c, cleanup := setupTestCache(t)
	defer cleanup()

	// 1. Setup a test server
	ts := testdata.NewTestServer(t, 1)
	defer ts.Close()

	// CRITICAL: We must ensure NarInfoHash == NarHash to cause the collision in upstreamJobs map.
	// The deadlock happens because pullNarInfo starts a job with key=hash, and then prePullNar
	// tries to start a job with key=hash (derived from URL).

	// NarInfoHash is 32 chars.
	// narURL.Hash comes from URL.
	// We want narURL.Hash == NarInfoHash.
	collisionHash := "11111111111111111111111111111111"

	// NarHash in text MUST be valid 52 chars (base32 sha256) to pass narinfo parser.
	// validNarHash := "1111111111111111111111111111111111111111111111111111"

	entry := testdata.Entry{
		NarInfoHash:    collisionHash,
		NarHash:        collisionHash, // Used for filename generation? No, URL determines it.
		NarCompression: "none",
		// We use a valid-looking NarInfo but point URL to the collision hash + .nar.
		// NarHash field is distinct (valid length) but URL hash causes the collision.
		NarInfoText: `StorePath: /nix/store/11111111111111111111111111111111-test-1.0
URL: nar/11111111111111111111111111111111.nar
Compression: none
FileHash: sha256:1111111111111111111111111111111111111111111111111111
FileSize: 123
NarHash: sha256:1111111111111111111111111111111111111111111111111111
NarSize: 123
References: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-dummy
Deriver: dddddddddddddddddddddddddddddddd-test-1.0.drv
Sig: cache.nixos.org-1:MadTCU1OSFCGUw4aqCKpLCZJpqBc7AbLvO7wgdlls0eq1DwaSnF/82SZE+wJGEiwlHbnZR+14daSaec0W3XoBQ==
`,
		NarText: "content-of-the-nar",
	}
	ts.AddEntry(entry)

	// Add debug handler to see what's being requested and serve content manually
	ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		// t.Logf("Server received request: %s %s", r.Method, r.URL.Path)
		if r.URL.Path == "/"+collisionHash+".narinfo" {
			// t.Log("Serving NarInfo manually")
			// w.Header().Set("Content-Length", "123") // Let http server handle it
			_, _ = w.Write([]byte(entry.NarInfoText))

			return true
		}

		if r.URL.Path == "/nar/"+collisionHash+".nar" {
			// t.Log("Serving Nar manually")
			// w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte(entry.NarText))

			return true
		}

		return false // Let the real handler process other things
	})

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)

	// Wait for health check
	select {
	case <-c.GetHealthChecker().Trigger():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for upstream health check")
	}

	// 2. Trigger the download
	// We use a timeout to detect the deadlock
	ctx, cancel := context.WithTimeout(newContext(), 5*time.Second)
	defer cancel()

	// detect a hang
	var narInfo *narinfo.NarInfo

	go func() {
		narInfo, err = c.GetNarInfo(ctx, entry.NarInfoHash)

		cancel()
	}()

Loop:
	for {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("Deadlock detected! GetNarInfo timed out.")
			}

			break Loop
		case <-time.After(10 * time.Second):
			cancel()
		}
	}

	require.NoError(t, err)
	assert.NotNil(t, narInfo)
}
