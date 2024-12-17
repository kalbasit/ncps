package local_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("path is required", func(t *testing.T) {
		t.Parallel()

		_, err := local.New(newContext(), "")
		assert.ErrorIs(t, err, local.ErrPathMustBeAbsolute)
	})

	t.Run("path is not absolute", func(t *testing.T) {
		t.Parallel()

		_, err := local.New(newContext(), "somedir")
		assert.ErrorIs(t, err, local.ErrPathMustBeAbsolute)
	})

	t.Run("path must exist", func(t *testing.T) {
		t.Parallel()

		_, err := local.New(newContext(), "/non-existing")
		assert.ErrorIs(t, err, local.ErrPathMustExist)
	})

	t.Run("path must be a directory", func(t *testing.T) {
		t.Parallel()

		f, err := os.CreateTemp("", "somefile")
		require.NoError(t, err)
		defer os.Remove(f.Name())

		_, err = local.New(newContext(), f.Name())
		assert.ErrorIs(t, err, local.ErrPathMustBeADirectory)
	})

	t.Run("path must be writable", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		require.NoError(t, os.Chmod(dir, 0o500))

		_, err = local.New(newContext(), dir)
		assert.ErrorIs(t, err, local.ErrPathMustBeWritable)
	})

	t.Run("valid path must return no error", func(t *testing.T) {
		t.Parallel()

		_, err := local.New(newContext(), os.TempDir())
		assert.NoError(t, err)
	})

	t.Run("should create directories", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		_, err = local.New(newContext(), dir)
		require.NoError(t, err)

		dirs := []string{
			"config",
			"store",
			filepath.Join("store", "narinfo"),
			filepath.Join("store", "nar"),
			filepath.Join("store", "tmp"),
		}

		for _, p := range dirs {
			t.Run("Checking that "+p+" exists", func(t *testing.T) {
				assert.DirExists(t, filepath.Join(dir, p))
			})
		}
	})

	t.Run("store/tmp is removed on boot", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		// create the directory tmp and add a file inside of it
		err = os.MkdirAll(filepath.Join(dir, "store", "tmp"), 0o700)
		require.NoError(t, err)

		f, err := os.CreateTemp(filepath.Join(dir, "store", "tmp"), "hello")
		require.NoError(t, err)

		_, err = local.New(newContext(), dir)
		require.NoError(t, err)

		assert.NoFileExists(t, f.Name())
	})
}

func TestGetSecretKey(t *testing.T) {
	t.Parallel()

	t.Run("no secret key is present", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		s, err := local.New(newContext(), dir)
		require.NoError(t, err)

		_, err = s.GetSecretKey(newContext())
		assert.ErrorIs(t, err, local.ErrNoSecretKey)
	})

	t.Run("secret key is present", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk1.String()), 0o400))

		sk2, err := s.GetSecretKey(ctx)
		require.NoError(t, err)

		assert.Equal(t, sk1.String(), sk2.String())
	})
}

func TestPutSecretKey(t *testing.T) {
	t.Parallel()

	t.Run("no secret exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		require.NoError(t, s.PutSecretKey(ctx, sk1))

		skPath := filepath.Join(dir, "config", "cache.key")
		if assert.FileExists(t, skPath) {
			skc, err := os.ReadFile(skPath)
			require.NoError(t, err)

			sk2, err := signature.LoadSecretKey(string(skc))
			require.NoError(t, err)

			assert.Equal(t, sk1.String(), sk2.String())
		}
	})

	t.Run("secret exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk1.String()), 0o400))

		sk2, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		err = s.PutSecretKey(ctx, sk2)
		assert.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteSecretKey(t *testing.T) {
	t.Parallel()

	t.Run("secret key does not exist", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		s, err := local.New(newContext(), dir)
		require.NoError(t, err)

		err = s.DeleteSecretKey(newContext())
		assert.ErrorIs(t, err, local.ErrNoSecretKey)
	})

	t.Run("secret key does exist", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk.String()), 0o400))

		require.NoError(t, s.DeleteSecretKey(ctx))

		assert.NoFileExists(t, skPath)
	})
}

func TestGetNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		_, err = s.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narInfoPath := filepath.Join(
			dir,
			"store",
			"narinfo",
			testdata.Nar1.NarInfoPath,
		)

		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))

		err = os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o400)
		require.NoError(t, err)

		ni, err := s.GetNarInfo(ctx, testdata.Nar1.NarInfoHash)
		require.NoError(t, err)

		assert.Equal(t,
			strings.TrimSpace(testdata.Nar1.NarInfoText),
			strings.TrimSpace(ni.String()),
		)
	})
}

func TestPutNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		ni1, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		require.NoError(t, s.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, ni1))

		narInfoPath := filepath.Join(
			dir,
			"store",
			"narinfo",
			testdata.Nar1.NarInfoPath,
		)

		require.FileExists(t, narInfoPath)

		ni2c, err := os.Open(narInfoPath)
		require.NoError(t, err)

		defer ni2c.Close()

		ni2, err := narinfo.Parse(ni2c)
		require.NoError(t, err)

		assert.Equal(t,
			strings.TrimSpace(ni1.String()),
			strings.TrimSpace(ni2.String()),
		)
	})

	t.Run("narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narInfoPath := filepath.Join(
			dir,
			"store",
			"narinfo",
			testdata.Nar1.NarInfoPath,
		)

		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))

		err = os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o400)
		require.NoError(t, err)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		err = s.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, ni)
		assert.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		assert.ErrorIs(t,
			s.DeleteNarInfo(ctx, testdata.Nar1.NarInfoHash),
			storage.ErrNotFound,
		)
	})

	t.Run("narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narInfoPath := filepath.Join(
			dir,
			"store",
			"narinfo",
			testdata.Nar1.NarInfoPath,
		)

		require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))

		err = os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o400)
		require.NoError(t, err)

		require.NoError(t, s.DeleteNarInfo(ctx, testdata.Nar1.NarInfoHash))

		assert.NoFileExists(t, narInfoPath)
	})
}
func TestGetNar(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		_, err = s.GetNar(ctx, nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		})

		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		defer os.RemoveAll(dir) // clean up

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narPath := filepath.Join(
			dir,
			"store",
			"nar",
			testdata.Nar1.NarPath,
		)

		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))

		err = os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o400)
		require.NoError(t, err)

		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		r, err := s.GetNar(ctx, narURL)
		require.NoError(t, err)

		nt, err := io.ReadAll(r)
		require.NoError(t, err)

		if assert.Equal(t, len(testdata.Nar1.NarText), len(nt)) {
			assert.Equal(t,
				strings.TrimSpace(testdata.Nar1.NarText),
				strings.TrimSpace(string(nt)),
			)
		}
	})
}

// func TestPutNar(t *testing.T) {
// 	t.Parallel()
//
// 	t.Run("no narfile exists in the store", func(t *testing.T) {
// 		t.Parallel()
//
// 		dir, err := os.MkdirTemp("", "cache-path-")
// 		require.NoError(t, err)
// 		defer os.RemoveAll(dir) // clean up
//
// 		ctx := newContext()
//
// 		s, err := local.New(ctx, dir)
// 		require.NoError(t, err)
//
// 		ni1, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarText))
// 		require.NoError(t, err)
//
// 		require.NoError(t, s.PutNar(ctx, testdata.Nar1.NarHash, ni1))
//
// 		narPath := filepath.Join(
// 			dir,
// 			"store",
// 			"narinfo",
// 			helper.NarFilePath(testdata.Nar1.NarHash),
// 		)
//
// 		require.FileExists(t, narPath)
//
// 		ni2c, err := os.Open(narPath)
// 		require.NoError(t, err)
//
// 		defer ni2c.Close()
//
// 		ni2, err := narinfo.Parse(ni2c)
// 		require.NoError(t, err)
//
// 		assert.Equal(t,
// 			strings.TrimSpace(ni1.String()),
// 			strings.TrimSpace(ni2.String()),
// 		)
// 	})
//
// 	t.Run("narfile exists in the store", func(t *testing.T) {
// 		t.Parallel()
//
// 		dir, err := os.MkdirTemp("", "cache-path-")
// 		require.NoError(t, err)
// 		defer os.RemoveAll(dir) // clean up
//
// 		ctx := newContext()
//
// 		s, err := local.New(ctx, dir)
// 		require.NoError(t, err)
//
// 		narPath := filepath.Join(
// 			dir,
// 			"store",
// 			"narinfo",
// 			helper.NarFilePath(testdata.Nar1.NarHash),
// 		)
//
// 		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
//
// 		err = os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o400)
// 		require.NoError(t, err)
//
// 		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarText))
// 		require.NoError(t, err)
//
// 		err = s.PutNar(ctx, testdata.Nar1.NarHash, ni)
// 		assert.ErrorIs(t, err, storage.ErrAlreadyExists)
// 	})
// }
//
// func TestDeleteNar(t *testing.T) {
// 	t.Parallel()
//
// 	t.Run("no nar exists in the store", func(t *testing.T) {
// 		t.Parallel()
//
// 		dir, err := os.MkdirTemp("", "cache-path-")
// 		require.NoError(t, err)
// 		defer os.RemoveAll(dir) // clean up
//
// 		ctx := newContext()
//
// 		s, err := local.New(ctx, dir)
// 		require.NoError(t, err)
//
// 		assert.ErrorIs(t,
// 			s.DeleteNar(ctx, testdata.Nar1.NarHash),
// 			storage.ErrNotFound,
// 		)
// 	})
//
// 	t.Run("narfile exists in the store", func(t *testing.T) {
// 		t.Parallel()
//
// 		dir, err := os.MkdirTemp("", "cache-path-")
// 		require.NoError(t, err)
// 		defer os.RemoveAll(dir) // clean up
//
// 		ctx := newContext()
//
// 		s, err := local.New(ctx, dir)
// 		require.NoError(t, err)
//
// 		narPath := filepath.Join(
// 			dir,
// 			"store",
// 			"narinfo",
// 			helper.NarFilePath(testdata.Nar1.NarHash),
// 		)
//
// 		require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
//
// 		err = os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o400)
// 		require.NoError(t, err)
//
// 		require.NoError(t, s.DeleteNar(ctx, testdata.Nar1.NarHash))
//
// 		assert.NoFileExists(t, narPath)
// 	})
// }

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
