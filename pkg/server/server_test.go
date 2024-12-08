package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/kalbasit/ncps/testdata"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

//nolint:paralleltest
func TestServeHTTP(t *testing.T) {
	t.Run("DELETE requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		if err != nil {
			t.Fatalf("expected no error, got: %q", err)
		}
		defer os.RemoveAll(dir) // clean up

		c, err := cache.New(logger, "cache.example.com", dir)
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

		t.Run("DELETE is not permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetDeletePermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
				if err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				resp, err := ts.Client().Do(r)
				if err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
					t.Errorf("want %d got %d", want, got)
				}
			})

			t.Run("nar", func(t *testing.T) {
				t.Run("without compression", func(t *testing.T) {
					url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar"

					r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("with compression", func(t *testing.T) {
					url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

					r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})
			})
		})

		t.Run("DELETE is permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetDeletePermitted(true)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

				t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err == nil {
						t.Fatal("expected an error but got none")
					}
				})

				f, err := os.Create(storePath)
				if err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				if _, err := f.WriteString(testdata.Nar1.NarInfoText); err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				if err := f.Close(); err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				t.Run("narinfo does exist in storage", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}
				})

				t.Run("DELETE returns no error", func(t *testing.T) {
					url := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if want, got := http.StatusNoContent, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("narinfo is gone from the store", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err == nil {
						t.Fatal("expected an error but got none")
					}
				})
			})

			t.Run("nar", func(t *testing.T) {
				t.Run("nar without compression", func(t *testing.T) {
					storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar")

					t.Run("nar does not exist in storage yet", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err == nil {
							t.Fatal("expected an error but got none")
						}
					})

					f, err := os.Create(storePath)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if _, err := f.WriteString(testdata.Nar1.NarText); err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if err := f.Close(); err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					t.Run("nar does exist in storage", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err != nil {
							t.Fatalf("expected no error but got: %s", err)
						}
					})

					t.Run("DELETE returns no error", func(t *testing.T) {
						url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar"

						r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
						if err != nil {
							t.Fatalf("expecting no error got %s", err)
						}

						resp, err := ts.Client().Do(r)
						if err != nil {
							t.Fatalf("expecting no error got %s", err)
						}

						if want, got := http.StatusNoContent, resp.StatusCode; want != got {
							t.Errorf("want %d got %d", want, got)
						}
					})

					t.Run("narinfo is gone from the store", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err == nil {
							t.Fatal("expected an error but got none")
						}
					})
				})

				t.Run("nar with compression", func(t *testing.T) {
					storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

					t.Run("nar does not exist in storage yet", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err == nil {
							t.Fatal("expected an error but got none")
						}
					})

					f, err := os.Create(storePath)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if _, err := f.WriteString(testdata.Nar1.NarText); err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if err := f.Close(); err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					t.Run("nar does exist in storage", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err != nil {
							t.Fatalf("expected no error but got: %s", err)
						}
					})

					t.Run("DELETE returns no error", func(t *testing.T) {
						url := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

						r, err := http.NewRequestWithContext(context.Background(), "DELETE", url, nil)
						if err != nil {
							t.Fatalf("expecting no error got %s", err)
						}

						resp, err := ts.Client().Do(r)
						if err != nil {
							t.Fatalf("expecting no error got %s", err)
						}

						if want, got := http.StatusNoContent, resp.StatusCode; want != got {
							t.Errorf("want %d got %d", want, got)
						}
					})

					t.Run("narinfo is gone from the store", func(t *testing.T) {
						_, err := os.Stat(storePath)
						if err == nil {
							t.Fatal("expected an error but got none")
						}
					})
				})
			})
		})
	})

	t.Run("GET requests", func(t *testing.T) {
		us := testdata.HTTPTestServer(t, 40)
		defer us.Close()

		uu, err := url.Parse(us.URL)
		if err != nil {
			t.Fatalf("error not expected, got %s", err)
		}

		dir, err := os.MkdirTemp("", "cache-path-")
		if err != nil {
			t.Fatalf("expected no error, got: %q", err)
		}
		defer os.RemoveAll(dir) // clean up

		uc, err := upstream.New(logger, uu.Host, []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
		if err != nil {
			t.Fatalf("expected no error, got %s", err)
		}

		c, err := cache.New(logger, "cache.example.com", dir)
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

		c.AddUpstreamCaches(uc)

		s := server.New(logger, c)

		t.Run("narinfo", func(t *testing.T) {
			t.Run("narinfo does not exist upstream", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/doesnotexist.narinfo", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				if want, got := http.StatusNotFound, w.Code; want != got {
					t.Errorf("want %d got %d", want, got)
				}
			})

			t.Run("narinfo exists upstream", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/"+testdata.Nar1.NarInfoHash+".narinfo", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				if want, got := http.StatusOK, w.Code; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("expected no error got %s", err)
				}

				// NOTE: HasPrefix instead equality because we add our signature to the narInfo.
				if !strings.HasPrefix(string(body), testdata.Nar1.NarInfoText) {
					t.Error("expected the body to start with narInfo but it did not")
				}
			})
		})

		t.Run("nar", func(t *testing.T) {
			t.Run("nar does not exist upstream", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/nar/doesnotexist.nar", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				if want, got := http.StatusNotFound, w.Code; want != got {
					t.Errorf("want %d got %d", want, got)
				}
			})

			t.Run("nar exists upstream without compression", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/nar/"+testdata.Nar1.NarHash+".nar", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				if want, got := http.StatusOK, w.Code; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("expected no error got %s", err)
				}

				if want, got := testdata.Nar1.NarText, string(body); want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})

			t.Run("nar exists upstream with compression", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/nar/"+testdata.Nar1.NarHash+".nar.xz", nil)
				w := httptest.NewRecorder()

				s.ServeHTTP(w, r)

				if want, got := http.StatusOK, w.Code; want != got {
					t.Errorf("want %d got %d", want, got)
				}

				resp := w.Result()
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("expected no error got %s", err)
				}

				if want, got := testdata.Nar1.NarText+"xz", string(body); want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})
	})

	t.Run("PUT requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		if err != nil {
			t.Fatalf("expected no error, got: %q", err)
		}
		defer os.RemoveAll(dir) // clean up

		c, err := cache.New(logger, "cache.example.com", dir)
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

		t.Run("PUT is not permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetPutPermitted(false)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				p := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarInfoText))
				if err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				resp, err := ts.Client().Do(r)
				if err != nil {
					t.Fatalf("expecting no error got %s", err)
				}

				if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
					t.Errorf("want %d got %d", want, got)
				}
			})

			t.Run("nar", func(t *testing.T) {
				t.Run("without compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("with compression", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarInfoHash + ".nar.xz"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("expecting no error got %s", err)
					}

					if want, got := http.StatusMethodNotAllowed, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})
			})
		})

		t.Run("PUT is permitted", func(t *testing.T) {
			s := server.New(logger, c)
			s.SetPutPermitted(true)

			ts := httptest.NewServer(s)
			defer ts.Close()

			t.Run("narInfo", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

				t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err == nil {
						t.Fatal("expected an error but got none")
					}
				})

				t.Run("putNarInfo does not return an error", func(t *testing.T) {
					p := ts.URL + "/" + testdata.Nar1.NarInfoHash + ".narinfo"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarInfoText))
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					if want, got := http.StatusNoContent, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("narinfo does exist in storage", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}
				})

				t.Run("it should be signed by our server", func(t *testing.T) {
					f, err := os.Open(storePath)
					if err != nil {
						t.Fatalf("no error was expected, got: %s", err)
					}

					ni, err := narinfo.Parse(f)
					if err != nil {
						t.Fatalf("no error was expected, got: %s", err)
					}

					var found bool

					var sig signature.Signature
					for _, sig = range ni.Signatures {
						if sig.Name == "cache.example.com" {
							found = true

							break
						}
					}

					if want, got := true, found; want != got {
						t.Errorf("want %t got %t", want, got)
					}

					validSig := signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()})

					if want, got := true, validSig; want != got {
						t.Errorf("want %t got %t", want, got)
					}
				})
			})

			t.Run("nar without compression", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar")

				t.Run("nar does not exist in storage yet", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err == nil {
						t.Fatal("expected an error but got none")
					}
				})

				t.Run("putNar does not return an error", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					if want, got := http.StatusNoContent, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("nar does exist in storage", func(t *testing.T) {
					f, err := os.Open(storePath)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}

					bs, err := io.ReadAll(f)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}

					if want, got := testdata.Nar1.NarText, string(bs); want != got {
						t.Errorf("want %q got %q", want, got)
					}
				})
			})

			t.Run("nar with compression", func(t *testing.T) {
				storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

				t.Run("nar does not exist in storage yet", func(t *testing.T) {
					_, err := os.Stat(storePath)
					if err == nil {
						t.Fatal("expected an error but got none")
					}
				})

				t.Run("putNar does not return an error", func(t *testing.T) {
					p := ts.URL + "/nar/" + testdata.Nar1.NarHash + ".nar.xz"

					r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(testdata.Nar1.NarText))
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					resp, err := ts.Client().Do(r)
					if err != nil {
						t.Fatalf("error Do(r): %s", err)
					}

					if want, got := http.StatusNoContent, resp.StatusCode; want != got {
						t.Errorf("want %d got %d", want, got)
					}
				})

				t.Run("nar does exist in storage", func(t *testing.T) {
					f, err := os.Open(storePath)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}

					bs, err := io.ReadAll(f)
					if err != nil {
						t.Fatalf("expected no error but got: %s", err)
					}

					if want, got := testdata.Nar1.NarText, string(bs); want != got {
						t.Errorf("want %q got %q", want, got)
					}
				})
			})
		})
	})
}
