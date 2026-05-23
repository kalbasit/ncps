package upstream_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestNew(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	//nolint:paralleltest
	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := upstream.New(newContext(), nil, nil)
			assert.ErrorIs(t, err, upstream.ErrURLRequired)
		})

		//nolint:paralleltest
		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, "cache.nixos.org"), nil)
			assert.ErrorIs(t, err, upstream.ErrURLMustContainScheme)
		})

		t.Run("valid url with no path must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})

		t.Run("valid url with only / must not return no error", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), nil)

			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("public keys", func(t *testing.T) {
		//nolint:paralleltest
		t.Run("invalid public keys", func(t *testing.T) {
			_, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
				PublicKeys: []string{"invalid"},
			})
			assert.True(t, strings.HasPrefix(err.Error(), "error parsing the public key: public key is corrupt:"))
		})

		//nolint:paralleltest
		t.Run("valid public keys", func(t *testing.T) {
			_, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				&upstream.Options{
					PublicKeys: testdata.PublicKeys(),
				},
			)
			assert.NoError(t, err)
		})
	})

	//nolint:paralleltest
	t.Run("priority parsed", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority parsed from URL", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=42"),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		require.NoError(t, err)

		assert.EqualValues(t, 42, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority in URL is zero", func(t *testing.T) {
		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=0"),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		require.NoError(t, err)

		assert.EqualValues(t, 40, c.GetPriority())
	})

	//nolint:paralleltest
	t.Run("priority in URL is invalid", func(t *testing.T) {
		_, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL+"?priority=-1"),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		assert.ErrorContains(t, err, "error parsing the priority from the URL")
	})
}

func TestGetNarInfo(t *testing.T) {
	t.Parallel()

	testFn := func(withKeys bool) func(*testing.T) {
		return func(t *testing.T) {
			t.Parallel()

			var (
				c   *upstream.Cache
				err error
			)

			ts := testdata.NewTestServer(t, 40)
			t.Cleanup(ts.Close)

			opts := &upstream.Options{}
			if withKeys {
				opts.PublicKeys = testdata.PublicKeys()
			}

			c, err = upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				opts,
			)

			require.NoError(t, err)

			t.Run("hash not found", func(t *testing.T) {
				_, err := c.GetNarInfo(context.Background(), "abc123")
				assert.ErrorIs(t, err, upstream.ErrNotFound)
			})

			for i, narEntry := range testdata.Entries {
				t.Run(fmt.Sprintf("testing nar entry ID %d hash %s", i, narEntry.NarHash), func(t *testing.T) {
					t.Run("hash is found", func(t *testing.T) {
						ni, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
						require.NoError(t, err, "failed to get nar info:\n"+narEntry.NarInfoText)

						assert.EqualValues(t, len(narEntry.NarText), ni.FileSize)
					})

					if narEntry.NarInfoHash == "c12lxpykv6sld7a0sakcnr3y0la70x8w" {
						t.Run("narinfo is removed from nar url", func(t *testing.T) {
							ni, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
							require.NoError(t, err)

							expectedURL := "nar/09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0.nar"
							assert.Equal(t, expectedURL, ni.URL)
						})
					}
				})
			}

			t.Run("check has failed", func(t *testing.T) {
				idx := ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
					for _, entry := range testdata.Entries {
						if r.URL.Path == "/broken-"+entry.NarInfoHash+".narinfo" {
							// mutate the inside
							b := entry.NarInfoText
							b = strings.ReplaceAll(b, "References:", "References: notfound-path")

							_, err := w.Write([]byte(b))
							if err != nil {
								http.Error(w, err.Error(), http.StatusInternalServerError)
							}

							return true
						}
					}

					return false
				})

				defer ts.RemoveMaybeHandler(idx)

				hash := "broken-" + testdata.Nar1.NarInfoHash

				_, err = c.GetNarInfo(context.Background(), hash)
				assert.ErrorContains(t, err, "error while checking the narInfo: invalid Reference[0]: notfound-path")
			})

			for i, entry := range testdata.Entries {
				t.Run(fmt.Sprintf("check Nar%d does not fail", i+1), func(t *testing.T) {
					hash := entry.NarInfoHash

					_, err = c.GetNarInfo(context.Background(), hash)
					assert.NoError(t, err, "failed to get nar info:\n"+entry.NarInfoText)
				})
			}
		}
	}

	//nolint:paralleltest
	t.Run("upstream without public keys", testFn(false))

	//nolint:paralleltest
	t.Run("upstream with public keys", testFn(true))

	t.Run("response header timeout fires when server stalls before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys:            testdata.PublicKeys(),
				ResponseHeaderTimeout: 50 * time.Millisecond,
			},
		)
		require.NoError(t, err)

		_, err = c.GetNarInfo(context.Background(), "hash")
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestHasNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("narinfo does not exist", func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		require.NoError(t, err)

		exists, err := c.HasNarInfo(context.Background(), "abc123")
		require.NoError(t, err)

		assert.False(t, exists)
	})

	for i, narEntry := range testdata.Entries {
		t.Run(fmt.Sprintf("Nar%d should exist", i+1), func(t *testing.T) {
			t.Parallel()

			ts := testdata.NewTestServer(t, 40)
			t.Cleanup(ts.Close)

			c, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				&upstream.Options{
					PublicKeys: testdata.PublicKeys(),
				},
			)
			require.NoError(t, err)

			exists, err := c.HasNarInfo(context.Background(), narEntry.NarInfoHash)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}

	t.Run("response header timeout treated as not-found for HEAD requests", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys:            testdata.PublicKeys(),
				ResponseHeaderTimeout: 50 * time.Millisecond,
			},
		)
		require.NoError(t, err)

		exists, err := c.HasNarInfo(context.Background(), "hash")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestGetNar(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		&upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		},
	)
	require.NoError(t, err)

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		_, err := c.GetNar(context.Background(), nu)
		assert.ErrorIs(t, err, upstream.ErrNotFound)
	})

	for i, narEntry := range testdata.Entries {
		t.Run(fmt.Sprintf("testing nar entry ID %d hash %s", i, narEntry.NarHash), func(t *testing.T) {
			t.Parallel()

			t.Run("hash is found", func(t *testing.T) {
				t.Parallel()

				nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
				resp, err := c.GetNar(context.Background(), nu)
				require.NoError(t, err)

				defer resp.Body.Close()

				// GetNar transparently decompresses zstd-encoded responses, so the body
				// always contains raw bytes regardless of upstream encoding. Content-Length
				// is stripped after decompression since the decompressed size is unknown upfront.
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				assert.Equal(t, narEntry.NarText, string(body))
			})
		})
	}

	t.Run("response header timeout fires when server stalls before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys:            testdata.PublicKeys(),
				ResponseHeaderTimeout: 50 * time.Millisecond,
			},
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		_, err = c.GetNar(context.Background(), nu)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestHasNar(t *testing.T) {
	t.Parallel()

	t.Run("nar does not exist", func(t *testing.T) {
		t.Parallel()

		ts := testdata.NewTestServer(t, 40)
		t.Cleanup(ts.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
			},
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		exists, err := c.HasNar(context.Background(), nu)
		require.NoError(t, err)

		assert.False(t, exists)
	})

	for i, narEntry := range testdata.Entries {
		t.Run(fmt.Sprintf("Nar%d should exist", i+1), func(t *testing.T) {
			t.Parallel()

			ts := testdata.NewTestServer(t, 40)
			t.Cleanup(ts.Close)

			c, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				&upstream.Options{
					PublicKeys: testdata.PublicKeys(),
				},
			)
			require.NoError(t, err)

			nu := nar.URL{Hash: narEntry.NarHash, Compression: narEntry.NarCompression}
			exists, err := c.HasNar(context.Background(), nu)
			require.NoError(t, err)

			assert.True(t, exists)
		})
	}

	t.Run("response header timeout treated as not-found for HEAD requests", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys:            testdata.PublicKeys(),
				ResponseHeaderTimeout: 50 * time.Millisecond,
			},
		)
		require.NoError(t, err)

		nu := nar.URL{Hash: "abc123", Compression: nar.CompressionTypeXz}
		exists, err := c.HasNar(context.Background(), nu)
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestGetNarCanMutate(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	c, err := upstream.New(
		newContext(),
		testhelper.MustParseURL(t, ts.URL),
		&upstream.Options{
			PublicKeys: testdata.PublicKeys(),
		},
	)
	require.NoError(t, err)

	pingV := testhelper.MustRandString(10)

	mutator := func(r *http.Request) {
		r.Header.Set("ping", pingV)
	}

	nu := nar.URL{Hash: testdata.Nar1.NarHash, Compression: nar.CompressionTypeXz}
	resp, err := c.GetNar(context.Background(), nu, mutator)
	require.NoError(t, err)

	defer func() {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	assert.Equal(t, pingV, resp.Header.Get("pong"))
}

// basicAuth is a middleware function that checks for basic authentication credentials.
func basicAuth(expectedUser, expectedPass string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()

		if !ok || user != expectedUser || pass != expectedPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)

			return
		}

		next(w, r)
	}
}

func TestNetrcAuthentication(t *testing.T) {
	t.Parallel()

	protectedHandler := func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30")
	}

	t.Run("WithCorrectCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		t.Cleanup(ts.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			&upstream.Options{
				NetrcCredentials: &upstream.NetrcCredentials{
					Username: "testuser",
					Password: "testpass",
				},
			},
		)
		require.NoError(t, err)

		priority, err := c.ParsePriority(newContext())
		require.NoError(t, err)
		assert.Equal(t, uint64(30), priority)
	})

	t.Run("WithoutCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		t.Cleanup(ts.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			nil,
		)
		require.NoError(t, err)

		_, err = c.ParsePriority(newContext())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected HTTP status code")
	})

	t.Run("WithIncorrectCredentials", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(basicAuth("testuser", "testpass", protectedHandler))
		t.Cleanup(ts.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, ts.URL),
			&upstream.Options{
				NetrcCredentials: &upstream.NetrcCredentials{
					Username: "testuser",
					Password: "wrongpass",
				},
			},
		)
		require.NoError(t, err)

		_, err = c.ParsePriority(newContext())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected HTTP status code")
	})
}

func TestNewWithOptions(t *testing.T) {
	t.Parallel()

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	testCases := []struct {
		name string
		opts *upstream.Options
	}{
		{
			name: "default timeouts when opts is nil",
			opts: nil,
		},
		{
			name: "default timeouts when opts fields are zero",
			opts: &upstream.Options{},
		},
		{
			name: "custom dialer timeout",
			opts: &upstream.Options{
				DialerTimeout: 10 * time.Second,
			},
		},
		{
			name: "custom response header timeout",
			opts: &upstream.Options{
				ResponseHeaderTimeout: 10 * time.Second,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := upstream.New(
				newContext(),
				testhelper.MustParseURL(t, ts.URL),
				tc.opts,
			)
			require.NoError(t, err)
			require.NotNil(t, c)
		})
	}

	t.Run("DialerTimeout fires on unreachable host", func(t *testing.T) {
		t.Parallel()

		// 192.0.2.1 is in TEST-NET-1 (RFC 5737), reserved for documentation.  On most
		// systems the TCP SYN gets no SYN-ACK and the dialer's timer fires ("timeout").
		// In a network-sandboxed build environment (e.g. Nix) the kernel may immediately
		// return ENETUNREACH ("network is unreachable") — both are valid outcomes; the
		// assertion below accepts either.  The elapsed-time guard catches regressions
		// where DialerTimeout is ignored and the request hangs on ResponseHeaderTimeout.
		const unroutable = "http://192.0.2.1:1"

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, unroutable),
			&upstream.Options{
				DialerTimeout:         50 * time.Millisecond,
				ResponseHeaderTimeout: 5 * time.Second,
			},
		)
		require.NoError(t, err)

		start := time.Now()
		_, err = c.GetNarInfo(context.Background(), "test")
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.True(t,
			errors.Is(err, context.DeadlineExceeded) ||
				strings.Contains(err.Error(), "timeout") ||
				strings.Contains(err.Error(), "unreachable"),
			"expected a connection-failure error, got: %v", err)
		// Bound at 2s to catch a regression where DialerTimeout is ignored and the request hangs
		// on ResponseHeaderTimeout (5s) or longer.
		assert.Less(t, elapsed, 2*time.Second, "DialerTimeout should fire well before ResponseHeaderTimeout")
	})

	t.Run("ResponseHeaderTimeout is wired through - short timeout fails, longer timeout succeeds", func(t *testing.T) {
		t.Parallel()

		// Server stalls 200ms before sending headers. A short ResponseHeaderTimeout (50ms) must
		// trip; a longer one (2s) must not.
		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "StorePath: /nix/store/test")
		}))
		t.Cleanup(slowServer.Close)

		cShort, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				ResponseHeaderTimeout: 50 * time.Millisecond,
			},
		)
		require.NoError(t, err)

		_, err = cShort.GetNarInfo(context.Background(), "test")
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)

		cLong, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				ResponseHeaderTimeout: 2 * time.Second,
			},
		)
		require.NoError(t, err)

		_, err = cLong.GetNarInfo(context.Background(), "test")
		require.Error(t, err)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
