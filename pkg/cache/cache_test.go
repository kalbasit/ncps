package cache

import (
	"os"
	"testing"
)

func TestNew(t *testing.T) {
	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Run("path is required", func(t *testing.T) {
			_, err := New("cache.example.com", "hello")
			if want, got := ErrPathMustBeAbsolute, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := New("cache.example.com", "hello")
			if want, got := ErrPathMustBeAbsolute, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := New("cache.example.com", "/non-existing")
			if want, got := ErrPathMustExist, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := New("cache.example.com", "/proc/cpuinfo")
			if want, got := ErrPathMustBeADirectory, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := New("cache.example.com", "/root")
			if want, got := ErrPathMustBeWritable, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := New("cache.example.com", os.TempDir())
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := New("", os.TempDir())
			if want, got := ErrHostnameRequired, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := New("https://cache.example.com", os.TempDir())
			if want, got := ErrHostnameMustNotContainScheme, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := New("cache.example.com/path/to", os.TempDir())
			if want, got := ErrHostnameMustNotContainPath, err; want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := New("cache.example.com", os.TempDir())
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})
}

// func TestPublicKey(t *testing.T) {
// 	c, err := New("cache.example.com", "/tmp")
// 	if err != nil {
// 		t.Fatalf("error not expected, got an error: %s", err)
// 	}
//
// 	if want, got := "cache.example.com:7ZN4u/26neOAo/bJnSPD3yGXeLbicArUs71ZNjdZ5I8=", c.PublicKey(); want != got {
// 		t.Errorf("want %q, got %q", want, got)
// 	}
// }
