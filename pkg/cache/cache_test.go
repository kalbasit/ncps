package cache_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/stretchr/testify/assert"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testdata"

	// Import the SQLite driver.
	_ "github.com/mattn/go-sqlite3"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

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

		t.Run("should create directories", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			if _, err = cache.New(logger, "cache.example.com", dir, nil); err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			dirs := []string{
				"config",
				"store",
				filepath.Join("store", "nar"),
				filepath.Join("store", "tmp"),
				filepath.Join("var", "ncps", "db"),
			}

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
			dir, err := os.MkdirTemp("", "cache-path-")
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

		t.Run("should create sqlite3 database", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			if _, err = cache.New(logger, "cache.example.com", dir, nil); err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			if _, err := os.Stat(filepath.Join(dir, "var", "ncps", "db", "db.sqlite")); err != nil {
				t.Fatalf("expected no error, got: %s", err)
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
	ts := testdata.HTTPTestServer(t, 40)
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

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	if err != nil {
		t.Fatalf("error opening the database: %s", err)
	}

	t.Run("narinfo does not exist upstream", func(t *testing.T) {
		_, err := c.GetNarInfo("doesnotexist")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("narinfo exists upstream", func(t *testing.T) {
		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo"))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", testdata.Nar2.NarHash+".nar.xz"))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM narinfos")
			if err != nil {
				t.Fatalf("error executing select query: %s", err)
			}

			var hashes []string

			for rows.Next() {
				var hash string

				if err := rows.Scan(&hash); err != nil {
					t.Fatalf("error fetching hash from db: %s", err)
				}

				hashes = append(hashes, hash)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 0, len(hashes); want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("nar does not exist in the database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM nars")
			if err != nil {
				t.Fatalf("error executing select query: %s", err)
			}

			var hashes []string

			for rows.Next() {
				var hash string

				if err := rows.Scan(&hash); err != nil {
					t.Fatalf("error fetching hash from db: %s", err)
				}

				hashes = append(hashes, hash)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 0, len(hashes); want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		ni, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}

		t.Run("size is correct", func(t *testing.T) {
			if want, got := uint64(50308), ni.FileSize; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo"))
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

		t.Run("it should have also pulled the nar", func(t *testing.T) {
			// Force the other goroutine to run so it actually download the file
			// Try at least 10 times before announcing an error
			var err error

			for i := 0; i < 9; i++ {
				// NOTE: I tried runtime.Gosched() but it makes the test flaky
				time.Sleep(time.Millisecond)

				_, err = os.Stat(filepath.Join(dir, "store", "nar", testdata.Nar2.NarHash+".nar.xz"))
				if err == nil {
					break
				}
			}

			if err != nil {
				t.Errorf("expected no error got %s", err)
			}
		})

		t.Run("narinfo does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

			rows, err := db.Query(query)
			if err != nil {
				t.Fatalf("error selecting narinfos: %s", err)
			}

			nims := make([]database.NarInfoModel, 0)

			for rows.Next() {
				var nim database.NarInfoModel

				if err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt); err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				nims = append(nims, nim)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := testdata.Nar2.NarInfoHash, nims[0].Hash; want != got {
				t.Errorf("want %q got %q", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
			}
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			if err != nil {
				t.Fatalf("error selecting narinfos: %s", err)
			}

			nims := make([]database.NarModel, 0)

			for rows.Next() {
				var nim database.NarModel

				err := rows.Scan(
					&nim.Hash,
					&nim.CreatedAt,
					&nim.LastAccessedAt,
				)
				if err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				nims = append(nims, nim)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := testdata.Nar2.NarHash, nims[0].Hash; want != got {
				t.Errorf("want %q got %q", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
			}
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.Query(query)
				if err != nil {
					t.Fatalf("error selecting narinfos: %s", err)
				}

				nims := make([]database.NarInfoModel, 0)

				for rows.Next() {
					var nim database.NarInfoModel

					if err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt); err != nil {
						t.Fatalf("expected no error got: %s", err)
					}

					nims = append(nims, nim)
				}

				if err := rows.Err(); err != nil {
					t.Errorf("not expecting an error got: %s", err)
				}

				if want, got := 1, len(nims); want != got {
					t.Fatalf("want %d got %d", want, got)
				}

				if want, got := testdata.Nar2.NarInfoHash, nims[0].Hash; want != got {
					t.Errorf("want %q got %q", want, got)
				}

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
					t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
				}
			})
		})

		t.Run("pulling it another time should update last_accessed_at only for narinfo", func(t *testing.T) {
			time.Sleep(time.Second)

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
			SELECT  hash, created_at,  last_accessed_at
			FROM narinfos
			`

				rows, err := db.Query(query)
				if err != nil {
					t.Fatalf("error selecting narinfos: %s", err)
				}

				nims := make([]database.NarInfoModel, 0)

				for rows.Next() {
					var nim database.NarInfoModel

					if err := rows.Scan(&nim.Hash, &nim.CreatedAt, &nim.LastAccessedAt); err != nil {
						t.Fatalf("expected no error got: %s", err)
					}

					nims = append(nims, nim)
				}

				if err := rows.Err(); err != nil {
					t.Errorf("not expecting an error got: %s", err)
				}

				if want, got := 1, len(nims); want != got {
					t.Fatalf("want %d got %d", want, got)
				}

				if want, got := testdata.Nar2.NarInfoHash, nims[0].Hash; want != got {
					t.Errorf("want %q got %q", want, got)
				}

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() == got.Unix() {
					t.Errorf("expected created_at != last_accessed_at got: %q == %q", want, got)
				}
			})
		})

		t.Run("no error is returned if the entry already exist in the database", func(t *testing.T) {
			if err := os.Remove(filepath.Join(dir, "store", testdata.Nar2.NarInfoHash+".narinfo")); err != nil {
				t.Fatalf("error removing the narinfo from the store: %s", err)
			}

			_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
			if err != nil {
				t.Errorf("no error expected, got: %s", err)
			}
		})

		t.Run("nar does not exist in storage, it gets pulled automatically", func(t *testing.T) {
			narFile := filepath.Join(dir, "store", "nar", testdata.Nar2.NarHash+".nar.xz")

			if err := os.Remove(narFile); err != nil {
				t.Fatalf("error remove the nar file: %s", err)
			}

			t.Run("it should not return an error", func(t *testing.T) {
				_, err := c.GetNarInfo(testdata.Nar2.NarInfoHash)
				if err != nil {
					t.Fatalf("no error expected, got: %s", err)
				}
			})

			t.Run("it should have also pulled the nar", func(t *testing.T) {
				// Force the other goroutine to run so it actually download the file
				// Try at least 10 times before announcing an error
				var err error

				for i := 0; i < 9; i++ {
					// NOTE: I tried runtime.Gosched() but it makes the test flaky
					time.Sleep(time.Millisecond)

					_, err = os.Stat(narFile)
					if err == nil {
						break
					}
				}

				if err != nil {
					t.Errorf("expected no error got %s", err)
				}
			})
		})
	})
}

//nolint:paralleltest
func TestPutNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	if err != nil {
		t.Fatalf("error opening the database: %s", err)
	}

	storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

	t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})

	t.Run("narinfo does not exist in the database yet", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM narinfos")
		if err != nil {
			t.Fatalf("error executing select query: %s", err)
		}

		var hashes []string

		for rows.Next() {
			var hash string

			if err := rows.Scan(&hash); err != nil {
				t.Fatalf("error fetching hash from db: %s", err)
			}

			hashes = append(hashes, hash)
		}

		if err := rows.Err(); err != nil {
			t.Errorf("not expecting an error got: %s", err)
		}

		if want, got := 0, len(hashes); want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("nar does not exist in the database yet", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM nars")
		if err != nil {
			t.Fatalf("error executing select query: %s", err)
		}

		var hashes []string

		for rows.Next() {
			var hash string

			if err := rows.Scan(&hash); err != nil {
				t.Fatalf("error fetching hash from db: %s", err)
			}

			hashes = append(hashes, hash)
		}

		if err := rows.Err(); err != nil {
			t.Errorf("not expecting an error got: %s", err)
		}

		if want, got := 0, len(hashes); want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("PutNarInfo does not return an error", func(t *testing.T) {
		r := io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText))

		err := c.PutNarInfo(context.Background(), testdata.Nar1.NarInfoHash, r)
		if err != nil {
			t.Errorf("error not expected got %s", err)
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

	t.Run("narinfo does exist in the database", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM narinfos")
		if err != nil {
			t.Fatalf("error executing select query: %s", err)
		}

		var hashes []string

		for rows.Next() {
			var hash string

			if err := rows.Scan(&hash); err != nil {
				t.Fatalf("error fetching hash from db: %s", err)
			}

			hashes = append(hashes, hash)
		}

		if err := rows.Err(); err != nil {
			t.Errorf("not expecting an error got: %s", err)
		}

		if want, got := 1, len(hashes); want != got {
			t.Fatalf("want %d got %d", want, got)
		}

		if want, got := testdata.Nar1.NarInfoHash, hashes[0]; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})

	t.Run("nar does exist in the database", func(t *testing.T) {
		rows, err := db.Query("SELECT hash FROM nars")
		if err != nil {
			t.Fatalf("error executing select query: %s", err)
		}

		var hashes []string

		for rows.Next() {
			var hash string

			if err := rows.Scan(&hash); err != nil {
				t.Fatalf("error fetching hash from db: %s", err)
			}

			hashes = append(hashes, hash)
		}

		if err := rows.Err(); err != nil {
			t.Errorf("not expecting an error got: %s", err)
		}

		if want, got := 1, len(hashes); want != got {
			t.Fatalf("want %d got %d", want, got)
		}

		if want, got := testdata.Nar1.NarHash, hashes[0]; want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}

//nolint:paralleltest
func TestDeleteNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	t.Run("file does not exist in the store", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", testdata.Nar1.NarInfoHash+".narinfo")

		t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("DeleteNarInfo does return an error", func(t *testing.T) {
			err := c.DeleteNarInfo(context.Background(), testdata.Nar1.NarInfoHash)
			if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})
	})

	t.Run("file does exist in the store", func(t *testing.T) {
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

		t.Run("DeleteNarInfo does not return an error", func(t *testing.T) {
			if err := c.DeleteNarInfo(context.Background(), testdata.Nar1.NarInfoHash); err != nil {
				t.Errorf("error not expected got %s", err)
			}
		})

		t.Run("narinfo is gone from the store", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})
	})
}

//nolint:paralleltest
func TestGetNar(t *testing.T) {
	ts := testdata.HTTPTestServer(t, 40)
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

	uc, err := upstream.New(logger, tu.Host, testdata.PublicKeys())
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	db, err := sql.Open("sqlite3", filepath.Join(dir, "var", "ncps", "db", "db.sqlite"))
	if err != nil {
		t.Fatalf("error opening the database: %s", err)
	}

	t.Run("nar does not exist upstream", func(t *testing.T) {
		_, _, err := c.GetNar("doesnotexist", "xz")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	narName := testdata.Nar1.NarHash + ".nar.xz"

	t.Run("nar exists upstream", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar.xz", narName))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("nar does not exist in database yet", func(t *testing.T) {
			rows, err := db.Query("SELECT hash FROM nars")
			if err != nil {
				t.Fatalf("error executing select query: %s", err)
			}

			var hashes []string

			for rows.Next() {
				var hash string

				if err := rows.Scan(&hash); err != nil {
					t.Fatalf("error fetching hash from db: %s", err)
				}

				hashes = append(hashes, hash)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 0, len(hashes); want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("getting the narinfo so the record in the database now exists", func(t *testing.T) {
			_, err := c.GetNarInfo(testdata.Nar1.NarInfoHash)
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}
		})

		size, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}
		defer r.Close()

		t.Run("size is correct", func(t *testing.T) {
			if want, got := int64(len(testdata.Nar1.NarText)), size; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("body is the same", func(t *testing.T) {
			body, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("expected no error, got: %s", err)
			}

			if assert.Equal(t, len(testdata.Nar1.NarText), len(string(body))) {
				assert.Equal(t, testdata.Nar1.NarText, string(body))
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar.xz", narName))
			if err != nil {
				t.Fatalf("expected no error got %s", err)
			}
		})

		t.Run("getting the narinfo so the record in the database now exists", func(t *testing.T) {
			_, err := c.GetNarInfo(testdata.Nar1.NarInfoHash)
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}
		})

		t.Run("nar does exist in the database, and has initial last_accessed_at", func(t *testing.T) {
			const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

			rows, err := db.Query(query)
			if err != nil {
				t.Fatalf("error selecting narinfos: %s", err)
			}

			nims := make([]database.NarModel, 0)

			for rows.Next() {
				var nim database.NarModel

				err := rows.Scan(
					&nim.Hash,
					&nim.CreatedAt,
					&nim.LastAccessedAt,
				)
				if err != nil {
					t.Fatalf("expected no error got: %s", err)
				}

				nims = append(nims, nim)
			}

			if err := rows.Err(); err != nil {
				t.Errorf("not expecting an error got: %s", err)
			}

			if want, got := 1, len(nims); want != got {
				t.Fatalf("want %d got %d", want, got)
			}

			if want, got := testdata.Nar1.NarHash, nims[0].Hash; want != got {
				t.Errorf("want %q got %q", want, got)
			}

			if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
				t.Errorf("expected created_at == last_accessed_at got: %q == %q", want, got)
			}
		})

		t.Run("pulling it another time within recordAgeIgnoreTouch should not update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			c.SetRecordAgeIgnoreTouch(time.Hour)

			defer func() {
				c.SetRecordAgeIgnoreTouch(0)
			}()

			_, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}
			defer r.Close()

			t.Run("narinfo does exist in the database with the same last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				if err != nil {
					t.Fatalf("error selecting narinfos: %s", err)
				}

				nims := make([]database.NarModel, 0)

				for rows.Next() {
					var nim database.NarModel

					err := rows.Scan(
						&nim.Hash,
						&nim.CreatedAt,
						&nim.LastAccessedAt,
					)
					if err != nil {
						t.Fatalf("expected no error got: %s", err)
					}

					nims = append(nims, nim)
				}

				if err := rows.Err(); err != nil {
					t.Errorf("not expecting an error got: %s", err)
				}

				if want, got := 1, len(nims); want != got {
					t.Fatalf("want %d got %d", want, got)
				}

				if want, got := testdata.Nar1.NarHash, nims[0].Hash; want != got {
					t.Errorf("want %q got %q", want, got)
				}

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() != got.Unix() {
					t.Errorf("expected created_at == last_accessed_at got: %q != %q", want, got)
				}
			})
		})

		t.Run("pulling it another time should update last_accessed_at", func(t *testing.T) {
			time.Sleep(time.Second)

			_, r, err := c.GetNar(testdata.Nar1.NarHash, "xz")
			if err != nil {
				t.Fatalf("no error expected, got: %s", err)
			}
			defer r.Close()

			t.Run("narinfo does exist in the database, and has more recent last_accessed_at", func(t *testing.T) {
				const query = `
				SELECT  hash,  created_at,  last_accessed_at
				FROM nars
				`

				rows, err := db.Query(query)
				if err != nil {
					t.Fatalf("error selecting narinfos: %s", err)
				}

				nims := make([]database.NarModel, 0)

				for rows.Next() {
					var nim database.NarModel

					err := rows.Scan(
						&nim.Hash,
						&nim.CreatedAt,
						&nim.LastAccessedAt,
					)
					if err != nil {
						t.Fatalf("expected no error got: %s", err)
					}

					nims = append(nims, nim)
				}

				if err := rows.Err(); err != nil {
					t.Errorf("not expecting an error got: %s", err)
				}

				if want, got := 1, len(nims); want != got {
					t.Fatalf("want %d got %d", want, got)
				}

				if want, got := testdata.Nar1.NarHash, nims[0].Hash; want != got {
					t.Errorf("want %q got %q", want, got)
				}

				if want, got := nims[0].CreatedAt, nims[0].LastAccessedAt; want.Unix() == got.Unix() {
					t.Errorf("expected created_at != last_accessed_at got: %q == %q", want, got)
				}
			})
		})
	})
}

//nolint:paralleltest
func TestPutNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	t.Run("without compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("putNar does not return an error", func(t *testing.T) {
			r := io.NopCloser(strings.NewReader(testdata.Nar1.NarText))

			err := c.PutNar(context.Background(), testdata.Nar1.NarHash, "xz", r)
			if err != nil {
				t.Errorf("error not expected got %s", err)
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

	t.Run("with compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("putNar does not return an error", func(t *testing.T) {
			r := io.NopCloser(strings.NewReader(testdata.Nar1.NarText))

			err := c.PutNar(context.Background(), testdata.Nar1.NarHash, "xz", r)
			if err != nil {
				t.Errorf("error not expected got %s", err)
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
}

//nolint:paralleltest
func TestDeleteNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	c.SetRecordAgeIgnoreTouch(0)

	t.Run("without compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

		t.Run("file does not exist in the store", func(t *testing.T) {
			t.Run("nar does not exist in storage yet", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})

			t.Run("DeleteNar does return an error", func(t *testing.T) {
				err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz")
				if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})

		t.Run("file does exist in the store", func(t *testing.T) {
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

			t.Run("deleteNar does not return an error", func(t *testing.T) {
				if err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz"); err != nil {
					t.Errorf("error not expected got %s", err)
				}
			})

			t.Run("nar is gone from the store", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})
		})
	})

	t.Run("with compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarHash+".nar.xz")

		t.Run("file does not exist in the store", func(t *testing.T) {
			t.Run("nar does not exist in storage yet", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})

			t.Run("DeleteNar does return an error", func(t *testing.T) {
				err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz")
				if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
					t.Errorf("want %q got %q", want, got)
				}
			})
		})

		t.Run("file does exist in the store", func(t *testing.T) {
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

			t.Run("deleteNar does not return an error", func(t *testing.T) {
				if err := c.DeleteNar(context.Background(), testdata.Nar1.NarHash, "xz"); err != nil {
					t.Errorf("error not expected got %s", err)
				}
			})

			t.Run("nar is gone from the store", func(t *testing.T) {
				_, err := os.Stat(storePath)
				if err == nil {
					t.Fatal("expected an error but got none")
				}
			})
		})
	})
}
