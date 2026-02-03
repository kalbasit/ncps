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

const (
	cacheName      = "cache.example.com"
	testHashABC    = "abc123"
	testHashABC456 = "abc456"
	testHashACD    = "acd456"
	testHashXYZ    = "xyz789"
	testHashXYZ123 = "xyz123"
	testHashXYZ456 = "xyz456"
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
		t.Cleanup(func() { os.Remove(f.Name()) })

		_, err = local.New(newContext(), f.Name())
		assert.ErrorIs(t, err, local.ErrPathMustBeADirectory)
	})

	t.Run("path must be writable", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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
			//nolint:paralleltest
			t.Run("Checking that "+p+" exists", func(t *testing.T) {
				assert.DirExists(t, filepath.Join(dir, p))
			})
		}
	})

	t.Run("store/tmp is removed on boot", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

		s, err := local.New(newContext(), dir)
		require.NoError(t, err)

		_, err = s.GetSecretKey(newContext())
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("secret key is present", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair(cacheName, nil)
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

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair(cacheName, nil)
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

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk1.String()), 0o400))

		sk2, _, err := signature.GenerateKeypair(cacheName, nil)
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

		t.Cleanup(func() { os.RemoveAll(dir) })

		s, err := local.New(newContext(), dir)
		require.NoError(t, err)

		err = s.DeleteSecretKey(newContext())
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("secret key does exist", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk.String()), 0o400))

		require.NoError(t, s.DeleteSecretKey(ctx))

		assert.NoFileExists(t, skPath)
	})
}

func TestHasNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		assert.False(t, s.HasNarInfo(ctx, testdata.Nar1.NarInfoHash))
	})

	t.Run("narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		assert.True(t, s.HasNarInfo(ctx, testdata.Nar1.NarInfoHash))
	})
}

func TestGetNarInfo(t *testing.T) {
	t.Parallel()

	t.Run("no narfile exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		t.Cleanup(func() { os.RemoveAll(dir) })

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

func TestHasNar(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		assert.False(t, s.HasNar(ctx, narURL))
	})

	t.Run("nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		assert.True(t, s.HasNar(ctx, narURL))
	})
}

func TestGetNar(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		_, _, err = s.GetNar(ctx, narURL)

		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		size, r, err := s.GetNar(ctx, narURL)
		require.NoError(t, err)

		nt, err := io.ReadAll(r)
		require.NoError(t, err)

		assert.EqualValues(t, len(testdata.Nar1.NarText), size)

		if assert.Len(t, testdata.Nar1.NarText, len(nt)) {
			assert.Equal(t,
				strings.TrimSpace(testdata.Nar1.NarText),
				strings.TrimSpace(string(nt)),
			)
		}
	})
}

func TestPutNar(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		written, err := s.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		require.EqualValues(t, len([]byte(testdata.Nar1.NarText)), written)

		narPath := filepath.Join(
			dir,
			"store",
			"nar",
			testdata.Nar1.NarPath,
		)

		require.FileExists(t, narPath)

		cs, err := os.ReadFile(narPath)
		require.NoError(t, err)

		if assert.Len(t, testdata.Nar1.NarText, len(string(cs))) {
			assert.Equal(t,
				strings.TrimSpace(testdata.Nar1.NarText),
				strings.TrimSpace(string(cs)),
			)
		}
	})

	t.Run("nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		_, err = s.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		assert.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNar(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

		ctx := newContext()

		s, err := local.New(ctx, dir)
		require.NoError(t, err)

		narURL := nar.URL{
			Hash:        testdata.Nar1.NarHash,
			Compression: testdata.Nar1.NarCompression,
		}

		assert.ErrorIs(t,
			s.DeleteNar(ctx, narURL),
			storage.ErrNotFound,
		)
	})

	t.Run("nar exists in the store", func(t *testing.T) {
		t.Parallel()

		dir, err := os.MkdirTemp("", "cache-path-")
		require.NoError(t, err)

		t.Cleanup(func() { os.RemoveAll(dir) })

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

		require.NoError(t, s.DeleteNar(ctx, narURL))

		assert.NoFileExists(t, narPath)
	})
}

func TestDeleteSecretKey_RemovesEmptyConfigDirectory(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	sk, _, err := signature.GenerateKeypair(cacheName, nil)
	require.NoError(t, err)

	skPath := filepath.Join(dir, "config", "cache.key")

	require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))
	require.NoError(t, os.WriteFile(skPath, []byte(sk.String()), 0o400))

	// Delete the secret key
	require.NoError(t, s.DeleteSecretKey(ctx))

	// Verify file is deleted
	assert.NoFileExists(t, skPath)

	// Verify config directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "config"))
}

func TestDeleteNarInfo_RemovesEmptyParentDirectories(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Use a hash that will create a unique directory structure: abc123
	// This creates: store/narinfo/a/ab/abc123.narinfo
	hash := testHashABC
	narInfoPath := filepath.Join(
		dir,
		"store",
		"narinfo",
		"a",
		"ab",
		hash+".narinfo",
	)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath, []byte("test"), 0o400))

	// Delete the narinfo
	require.NoError(t, s.DeleteNarInfo(ctx, hash))

	// Verify file is deleted
	assert.NoFileExists(t, narInfoPath)

	// Verify ab/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "narinfo", "a", "ab"))

	// Verify a/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "narinfo", "a"))

	// Verify narinfo/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "narinfo"))
}

func TestDeleteNarInfo_PreservesNonEmptyDirectories(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Create two narinfo files in the same level-2 directory
	// abc123 and abc456 both go into a/ab/
	hash1 := testHashABC
	hash2 := testHashABC456

	narInfoPath1 := filepath.Join(
		dir,
		"store",
		"narinfo",
		"a",
		"ab",
		hash1+".narinfo",
	)
	narInfoPath2 := filepath.Join(
		dir,
		"store",
		"narinfo",
		"a",
		"ab",
		hash2+".narinfo",
	)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath1), 0o700))
	require.NoError(t, os.WriteFile(narInfoPath1, []byte("test1"), 0o400))
	require.NoError(t, os.WriteFile(narInfoPath2, []byte("test2"), 0o400))

	// Delete one narinfo
	require.NoError(t, s.DeleteNarInfo(ctx, hash1))

	// Verify deleted file is gone
	assert.NoFileExists(t, narInfoPath1)

	// Verify the other file still exists
	assert.FileExists(t, narInfoPath2)

	// Verify ab/ directory still exists (contains abc456.narinfo)
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo", "a", "ab"))

	// Verify a/ directory still exists
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo", "a"))

	// Verify narinfo/ directory still exists
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo"))
}

func TestDeleteNarInfo_PartialCleanup(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Create narinfo files in multiple level-2 dirs under same level-1
	// abc goes into a/ab/
	// acd goes into a/ac/
	hashAB := testHashABC
	hashAC := testHashACD

	narInfoPathAB := filepath.Join(
		dir,
		"store",
		"narinfo",
		"a",
		"ab",
		hashAB+".narinfo",
	)
	narInfoPathAC := filepath.Join(
		dir,
		"store",
		"narinfo",
		"a",
		"ac",
		hashAC+".narinfo",
	)

	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPathAB), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPathAC), 0o700))
	require.NoError(t, os.WriteFile(narInfoPathAB, []byte("test1"), 0o400))
	require.NoError(t, os.WriteFile(narInfoPathAC, []byte("test2"), 0o400))

	// Delete abc123
	require.NoError(t, s.DeleteNarInfo(ctx, hashAB))

	// Verify deleted file is gone
	assert.NoFileExists(t, narInfoPathAB)

	// Verify ab/ is removed (was empty)
	assert.NoDirExists(t, filepath.Join(dir, "store", "narinfo", "a", "ab"))

	// Verify ac/ still exists
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo", "a", "ac"))

	// Verify a/ still exists (contains ac/)
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo", "a"))

	// Verify narinfo/ still exists
	assert.DirExists(t, filepath.Join(dir, "store", "narinfo"))
}

func TestDeleteNar_RemovesEmptyParentDirectories(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Use a hash that will create a unique directory structure: xyz789
	// This creates: store/nar/x/xy/xyz789.nar.xz
	hash := testHashXYZ
	narPath := filepath.Join(
		dir,
		"store",
		"nar",
		"x",
		"xy",
		hash+".nar.xz",
	)

	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o700))
	require.NoError(t, os.WriteFile(narPath, []byte("test"), 0o400))

	narURL := nar.URL{
		Hash:        hash,
		Compression: nar.CompressionTypeXz,
	}

	// Delete the nar
	require.NoError(t, s.DeleteNar(ctx, narURL))

	// Verify file is deleted
	assert.NoFileExists(t, narPath)

	// Verify xy/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "nar", "x", "xy"))

	// Verify x/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "nar", "x"))

	// Verify nar/ directory is removed
	assert.NoDirExists(t, filepath.Join(dir, "store", "nar"))
}

func TestDeleteNar_PreservesNonEmptyDirectories(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)

	t.Cleanup(func() { os.RemoveAll(dir) })

	ctx := newContext()

	s, err := local.New(ctx, dir)
	require.NoError(t, err)

	// Create two nar files in the same level-2 directory
	// xyz123 and xyz456 both go into x/xy/
	hash1 := testHashXYZ123
	hash2 := testHashXYZ456

	narPath1 := filepath.Join(
		dir,
		"store",
		"nar",
		"x",
		"xy",
		hash1+".nar.xz",
	)
	narPath2 := filepath.Join(
		dir,
		"store",
		"nar",
		"x",
		"xy",
		hash2+".nar.zst",
	)

	require.NoError(t, os.MkdirAll(filepath.Dir(narPath1), 0o700))
	require.NoError(t, os.WriteFile(narPath1, []byte("test1"), 0o400))
	require.NoError(t, os.WriteFile(narPath2, []byte("test2"), 0o400))

	narURL1 := nar.URL{
		Hash:        hash1,
		Compression: nar.CompressionTypeXz,
	}

	// Delete one nar
	require.NoError(t, s.DeleteNar(ctx, narURL1))

	// Verify deleted file is gone
	assert.NoFileExists(t, narPath1)

	// Verify the other file still exists
	assert.FileExists(t, narPath2)

	// Verify xy/ directory still exists (contains xyz456.nar.zst)
	assert.DirExists(t, filepath.Join(dir, "store", "nar", "x", "xy"))

	// Verify x/ directory still exists
	assert.DirExists(t, filepath.Join(dir, "store", "nar", "x"))

	// Verify nar/ directory still exists
	assert.DirExists(t, filepath.Join(dir, "store", "nar"))
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
