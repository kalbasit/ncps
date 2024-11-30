package cache

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/kalbasit/ncps/pkg/upstreamcache"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

type Cache struct {
	hostname       string
	path           string
	secretKey      signature.SecretKey
	upstreamCaches []upstreamcache.UpstreamCache
}

// New returns a new Cache
func New(hostname, path string, ucs []upstreamcache.UpstreamCache) (Cache, error) {
	c := Cache{
		hostname:       hostname,
		path:           path,
		upstreamCaches: ucs,
	}

	sk, err := c.setupSecretKey()
	if err != nil {
		return c, fmt.Errorf("error setting up the secret key: %w", err)
	}

	c.secretKey = sk

	return c, nil
}

// PublicKey returns the public key of the server
func (c Cache) PublicKey() string { return c.secretKey.ToPublicKey().String() }

// GetFile retuns the file define by its key
// NOTE: It's the caller responsability to close the file after using it
func (c Cache) GetFile(key string) (io.ReadCloser, os.FileInfo, error) {
	f, err := os.Open(path.Join(c.path, key))
	if err != nil {
		return nil, nil, fmt.Errorf("error opening the file %q: %w", key, err)
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("error getting the file stat %q: %w", key, err)
	}

	return f, stat, nil
}

func (c Cache) configPath() string   { return path.Join(c.path, "config") }
func (c Cache) storePath() string    { return path.Join(c.path, "store") }
func (c Cache) cacheKeyPath() string { return path.Join(c.configPath(), "cache.key") }

func (c Cache) setupSecretKey() (signature.SecretKey, error) {
	f, err := os.Open(c.cacheKeyPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.createNewKey()
		}

		return signature.SecretKey{}, fmt.Errorf("error reading the secret key from %q: %w", err)
	}
	defer f.Close()

	skc, err := io.ReadAll(f)
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error reading the secret key from %q: %w", err)
	}

	sk, err := signature.LoadSecretKey(string(skc))
	if err != nil {
		return signature.SecretKey{}, fmt.Errorf("error loading the secret key: %w", err)
	}

	return sk, nil
}

func (c Cache) createNewKey() (signature.SecretKey, error) {
	secretKey, _, err := signature.GenerateKeypair(c.hostname, nil)
	if err != nil {
		return secretKey, fmt.Errorf("error generating a new secret key: %w", err)
	}

	if err := os.MkdirAll(path.Dir(c.cacheKeyPath()), 0700); err != nil {
		return secretKey, fmt.Errorf("error creating the parent directories for %q: %w", c.cacheKeyPath(), err)
	}

	f, err := os.Create(c.cacheKeyPath())
	if err != nil {
		return secretKey, fmt.Errorf("error creating the cache key file %q: %w", c.cacheKeyPath(), err)
	}

	defer f.Close()

	if _, err := f.WriteString(secretKey.String()); err != nil {
		return secretKey, fmt.Errorf("error writing the secret key to %q: %w", c.cacheKeyPath(), err)
	}

	return secretKey, nil
}
