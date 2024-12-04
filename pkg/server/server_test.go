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
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

const (
	nixStoreInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`

	narInfoHash = "7bn85d74qa0127p85rrswfyghxsqmcf7"

	//nolint:lll
	narInfoText = `StorePath: /nix/store/7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722
URL: nar/136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00.nar.xz
Compression: xz
FileHash: sha256:136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00
FileSize: 132228
NarHash: sha256:1rzb80kz42wy067pp160rridw41dnc09d2a3cqj2wdg6ylklhxkh
NarSize: 534160
References: 7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722 892cxk44qxzzlw9h90a781zpy1j7gmmn-libidn2-2.3.2 l25bc19is0s27929kxkfhgdzhc7x9g5m-libcap-2.49-lib rir9pf0kz1mb84x5bd3yr0fx415yy423-glibc-2.33-123
Deriver: 9fs4vq4gdsb8r9ywawb5f6zl40ycp1bh-iputils-20210722.drv
Sig: cache.nixos.org-1:WzhkqDdkgPz2qU/0QyEA6wUIm7EMR5MY8nTb5jAmmoh5b80ACIp/+Zpgi5t1KvmO8uG8GVrkPejCxbyQ2gNXDQ==
`

	narHash = "136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00"

	narText = "Hello, World" // fake nar for above nar info
)

//nolint:paralleltest
func TestServeHTTP(t *testing.T) {
	t.Run("DELETE requests", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "cache-path-")
		if err != nil {
			t.Fatalf("expected no error, got: %q", err)
		}
		defer os.RemoveAll(dir) // clean up

		c, err := cache.New(logger, "cache.example.com", dir, nil)
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

		s := server.New(logger, c)

		ts := httptest.NewServer(s)
		defer ts.Close()

		t.Run("narInfo", func(t *testing.T) {
			storePath := filepath.Join(dir, "store", narInfoHash+".narinfo")

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

			if _, err := f.WriteString(narInfoText); err != nil {
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
				r, err := http.NewRequestWithContext(context.Background(), "DELETE", ts.URL+"/"+narInfoHash+".narinfo", nil)
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
				storePath := filepath.Join(dir, "store", "nar", narHash+".nar")

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

				if _, err := f.WriteString(narText); err != nil {
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
					r, err := http.NewRequestWithContext(context.Background(), "DELETE", ts.URL+"/nar/"+narHash+".nar", nil)
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
				storePath := filepath.Join(dir, "store", "nar", narHash+".nar.xz")

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

				if _, err := f.WriteString(narText); err != nil {
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
					r, err := http.NewRequestWithContext(context.Background(), "DELETE", ts.URL+"/nar/"+narHash+".nar.xz", nil)
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

	t.Run("GET requests", func(t *testing.T) {
		us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/nix-cache-info" {
				if _, err := w.Write([]byte(nixStoreInfo)); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				return
			}

			if r.URL.Path == "/"+narInfoHash+".narinfo" {
				if _, err := w.Write([]byte(narInfoText)); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				return
			}

			if r.URL.Path == "/nar/"+narHash+".nar" {
				if _, err := w.Write([]byte(narText)); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				return
			}

			if r.URL.Path == "/nar/"+narHash+".nar.xz" {
				if _, err := w.Write([]byte(narText + "xz")); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				return
			}

			w.WriteHeader(http.StatusNotFound)
		}))
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

		c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

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
				r := httptest.NewRequest("GET", "/"+narInfoHash+".narinfo", nil)
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
				if !strings.HasPrefix(string(body), narInfoText) {
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
				r := httptest.NewRequest("GET", "/nar/"+narHash+".nar", nil)
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

				if want, got := narText, string(body); want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})

			t.Run("nar exists upstream with compression", func(t *testing.T) {
				r := httptest.NewRequest("GET", "/nar/"+narHash+".nar.xz", nil)
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

				if want, got := narText+"xz", string(body); want != got {
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

		c, err := cache.New(logger, "cache.example.com", dir, nil)
		if err != nil {
			t.Fatalf("expected no error, got %q", err)
		}

		s := server.New(logger, c)

		ts := httptest.NewServer(s)
		defer ts.Close()

		t.Run("narInfo", func(t *testing.T) {
			storePath := filepath.Join(dir, "store", narInfoHash+".narinfo")

			t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})

			t.Run("putNarInfo does not return an error", func(t *testing.T) {
				p := ts.URL + "/" + narInfoHash + ".narinfo"

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narInfoText))
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
			storePath := filepath.Join(dir, "store", "nar", narHash+".nar")

			t.Run("nar does not exist in storage yet", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})

			t.Run("putNar does not return an error", func(t *testing.T) {
				p := ts.URL + "/nar/" + narHash + ".nar"

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narText))
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

				if want, got := narText, string(bs); want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})

		t.Run("nar with compression", func(t *testing.T) {
			storePath := filepath.Join(dir, "store", "nar", narHash+".nar.xz")

			t.Run("nar does not exist in storage yet", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})

			t.Run("putNar does not return an error", func(t *testing.T) {
				p := ts.URL + "/nar/" + narHash + ".nar.xz"

				r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narText))
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

				if want, got := narText, string(bs); want != got {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})
	})
}
