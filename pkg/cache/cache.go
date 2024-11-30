package cache

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

type Cache struct {
	hostname  string
	path      string
	secretKey signature.SecretKey
}

// New returns a new Cache
func New(hostname, path string) (Cache, error) {
	c := Cache{
		hostname: hostname,
		path:     path,
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

func (c Cache) configPath() string    { return path.Join(c.path, "config") }
func (c Cache) secretKeyPath() string { return path.Join(c.configPath(), "cache.key") }

func (c Cache) setupSecretKey() (signature.SecretKey, error) {
	f, err := os.Open(c.secretKeyPath())
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
	if err := os.MkdirAll(path.Dir(c.secretKeyPath()), 0700); err != nil {
		return signature.SecretKey{}, fmt.Errorf("error creating the parent directories for %q: %w", c.secretKeyPath(), err)
	}

	secretKey, _, err := signature.GenerateKeypair(c.hostname, nil)
	if err != nil {
		return secretKey, fmt.Errorf("error generating a new secret key: %w", err)
	}

	f, err := os.Create(c.secretKeyPath())
	if err != nil {
		return secretKey, fmt.Errorf("error creating the cache key file %q: %w", c.secretKeyPath(), err)
	}

	defer f.Close()

	if _, err := f.WriteString(secretKey.String()); err != nil {
		return secretKey, fmt.Errorf("error writing the secret key to %q: %w", c.secretKeyPath(), err)
	}

	return secretKey, nil
}
