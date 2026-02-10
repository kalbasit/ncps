package upstream_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/helper"
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
						require.NoError(t, err)

						assert.EqualValues(t, len(narEntry.NarText), ni.FileSize)
					})

					if narEntry.NarInfoHash == "c12lxpykv6sld7a0sakcnr3y0la70x8w" {
						t.Run("narinfo is removed from nar url", func(t *testing.T) {
							ni, err := c.GetNarInfo(context.Background(), narEntry.NarInfoHash)
							require.NoError(t, err)

							assert.Equal(t, "nar/09xizkfyvigl5fqs0dhkn46nghfwwijbpdzzl4zg6kx90prjmsg0.nar", ni.URL)
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
					assert.NoError(t, err)
				})
			}
		}
	}

	//nolint:paralleltest
	t.Run("upstream without public keys", testFn(false))

	//nolint:paralleltest
	t.Run("upstream with public keys", testFn(true))

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
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

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
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

				defer func() {
					//nolint:errcheck
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}()

				assert.Equal(t, strconv.Itoa(len(narEntry.NarText)), resp.Header.Get("Content-Length"))
			})
		})
	}

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			&upstream.Options{
				PublicKeys: testdata.PublicKeys(),
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

	t.Run("timeout if server takes more than 3 seconds before first byte", func(t *testing.T) {
		t.Parallel()

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(slowServer.Close)

		c, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
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

	pingV := helper.MustRandString(10, nil)

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

	t.Run("custom dialer timeout is respected - slow connection succeeds with longer timeout", func(t *testing.T) {
		t.Parallel()

		// Create a listener that delays accepting connections
		//nolint:noctx // Using net.Listen is fine in tests
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { listener.Close() })

		slowListener := &slowAcceptListener{
			Listener: listener,
			delay:    4 * time.Second, // Longer than default 3s timeout
		}

		// Start a server with the slow listener
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "StorePath: /nix/store/test")
			}),
			ReadHeaderTimeout: 10 * time.Second,
		}

		go func() {
			//nolint:errcheck
			server.Serve(slowListener)
		}()

		// Allow the server goroutine to start before making a connection.
		time.Sleep(100 * time.Millisecond)
		t.Cleanup(func() { server.Close() })

		serverURL := fmt.Sprintf("http://%s", listener.Addr().String())

		// With default timeout (3s), connection should fail
		cDefault, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, serverURL),
			nil,
		)
		require.NoError(t, err)

		_, err = cDefault.GetNarInfo(context.Background(), "test")
		require.Error(t, err)
		// The error might be deadline exceeded or connection refused depending on timing
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout"))

		// With custom longer timeout (6s), connection should succeed
		opts := &upstream.Options{
			DialerTimeout:         6 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second, // Set this too so we don't timeout waiting for headers
		}

		cCustom, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, serverURL),
			opts,
		)
		require.NoError(t, err)

		// This should NOT timeout during connection because we have a longer timeout
		_, err = cCustom.GetNarInfo(context.Background(), "test")
		// It will error with parsing, but not with connection timeout
		require.Error(t, err)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("custom response header timeout is respected - slow server succeeds with longer timeout", func(t *testing.T) {
		t.Parallel()

		// Server that takes 4 seconds to respond (longer than default 3s timeout)
		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(4 * time.Second)
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "StorePath: /nix/store/test")
		}))
		t.Cleanup(slowServer.Close)

		// With default timeout (3s), this should fail
		cDefault, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			nil,
		)
		require.NoError(t, err)

		_, err = cDefault.GetNarInfo(context.Background(), "test")
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)

		// With custom longer timeout (6s), this should succeed
		opts := &upstream.Options{
			ResponseHeaderTimeout: 6 * time.Second,
		}

		cCustom, err := upstream.New(
			newContext(),
			testhelper.MustParseURL(t, slowServer.URL),
			opts,
		)
		require.NoError(t, err)

		// This should NOT timeout because we have a longer timeout
		_, err = cCustom.GetNarInfo(context.Background(), "test")
		// It will error with parsing, but not with timeout
		require.Error(t, err)
		assert.NotErrorIs(t, err, context.DeadlineExceeded)
	})
}

// slowAcceptListener wraps a net.Listener to delay accepting connections.
type slowAcceptListener struct {
	net.Listener
	delay time.Duration
}

func (l *slowAcceptListener) Accept() (net.Conn, error) {
	time.Sleep(l.delay)

	return l.Listener.Accept()
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
