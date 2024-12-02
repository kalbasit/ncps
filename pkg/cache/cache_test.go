package cache_test

import (
	"context"
	"errors"
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
Sig: cache.nixos.org-1:WzhkqDdkgPz2qU/0QyEA6wUIm7EMR5MY8nTb5jAmmoh5b80ACIp/+Zpgi5t1KvmO8uG8GVrkPejCxbyQ2gNXDQ==`

	narHash = "136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00"

	narText = "Hello, World" // fake nar for above nar info
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Parallel()

		t.Run("path is required", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/non-existing", nil)
			if want, got := cache.ErrPathMustExist, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/proc/cpuinfo", nil)
			if want, got := cache.ErrPathMustBeADirectory, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/root", nil)
			if want, got := cache.ErrPathMustBeWritable, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})

		t.Run("config/, store/nar and store/tmp were created", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			_, err = cache.New(logger, "cache.example.com", dir, nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			dirs := []string{"config", "store", filepath.Join("store", "nar"), filepath.Join("store", "tmp")}

			for _, p := range dirs {
				t.Run("Checking that "+p+" exists", func(t *testing.T) {
					info, err := os.Stat(filepath.Join(dir, p))
					if err != nil {
						t.Fatalf("expected no error, got: %s", err)
					}

					if want, got := true, info.IsDir(); want != got {
						t.Errorf("want %t got %t", want, got)
					}
				})
			}
		})

		t.Run("store/tmp is removed on boot", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			// create the directory tmp and add a file inside of it
			if err := os.MkdirAll(filepath.Join(dir, "store", "tmp"), 0o700); err != nil {
				t.Fatalf("expected no error but got %s", err)
			}

			f, err := os.CreateTemp(filepath.Join(dir, "store", "tmp"), "hello")
			if err != nil {
				t.Fatalf("expected no error but got %s", err)
			}

			_, err = cache.New(logger, "cache.example.com", dir, nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
				t.Errorf("expected %q to not exist but it does", f.Name())
			}
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := cache.New(logger, "", os.TempDir(), nil)
			if want, got := cache.ErrHostnameRequired, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := cache.New(logger, "https://cache.example.com", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainScheme, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com/path/to", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainPath, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	c, err := cache.New(logger, "cache.example.com", "/tmp", nil)
	if err != nil {
		t.Fatalf("error not expected, got an error: %s", err)
	}

	pubKey := c.PublicKey().String()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		t.Parallel()

		if !strings.HasPrefix(pubKey, "cache.example.com:") {
			t.Errorf("public key should start with cache.example.com: but it does not: %s", pubKey)
		}
	})

	t.Run("should return a valid public key", func(t *testing.T) {
		t.Parallel()

		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			t.Fatalf("error is not expected: %s", err)
		}

		if want, got := pubKey, pk.String(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}

//nolint:paralleltest
func TestGetNarInfo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("error not expected, got %s", err)
	}

	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	t.Run("narfile does not exist upstream", func(t *testing.T) {
		_, err := c.GetNarInfo(context.Background(), "doesnotexist")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("narfile exists upstream", func(t *testing.T) {
		t.Run("narfile does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", narInfoHash+".narinfo"))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		ni, err := c.GetNarInfo(context.Background(), narInfoHash)
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}

		t.Run("size is correct", func(t *testing.T) {
			if want, got := uint64(132228), ni.FileSize; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", narInfoHash+".narinfo"))
			if err != nil {
				t.Fatalf("expected no error got %s", err)
			}
		})

		t.Run("it should be signed by our server", func(t *testing.T) {
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
}

//nolint:paralleltest
func TestGetNar(t *testing.T) {
	narName := narHash + ".nar"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nix-cache-info" {
			if _, err := w.Write([]byte(nixStoreInfo)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		if r.URL.Path == "/nar/"+narName {
			if _, err := w.Write([]byte(narText)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("error not expected, got %s", err)
	}

	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	t.Run("nar does not exist upstream", func(t *testing.T) {
		_, _, err := c.GetNar(context.Background(), "doesnotexist", "")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("nar exists upstream", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar", narName))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		size, r, err := c.GetNar(context.Background(), narHash, "")
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}
		defer r.Close()

		t.Run("size is correct", func(t *testing.T) {
			if want, got := int64(len(narText)), size; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("body is the same", func(t *testing.T) {
			body, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("expected no error, got: %s", err)
			}

			if want, got := narText, string(body); want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar", narName))
			if err != nil {
				t.Fatalf("expected no error got %s", err)
			}
		})
	})
}
