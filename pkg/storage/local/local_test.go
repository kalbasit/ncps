package local_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/storage/local"
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

		s, err := local.New(newContext(), dir)
		require.NoError(t, err)

		sk1, _, err := signature.GenerateKeypair("cache.example.com", nil)
		require.NoError(t, err)

		skPath := filepath.Join(dir, "config", "cache.key")

		require.NoError(t, os.MkdirAll(filepath.Dir(skPath), 0o700))

		require.NoError(t, os.WriteFile(skPath, []byte(sk1.String()), 0o400))

		sk2, err := s.GetSecretKey(newContext())
		require.NoError(t, err)

		assert.Equal(t, sk1.String(), sk2.String())
	})
}

func TestPutSecretKey(t *testing.T) {
}

func TestDeleteSecretKey(t *testing.T) {
}

func TestGetNarInfo(t *testing.T) {
}

func TestPutNarInfo(t *testing.T) {
}

func TestDeleteNarInfo(t *testing.T) {
}

func TestGetNar(t *testing.T) {
}

func TestPutNar(t *testing.T) {
}

func TestDeleteNar(t *testing.T) {
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}
