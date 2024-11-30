package cache

import (
	"fmt"
	"io"
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
func New(hostname, path, secretKey string) (Cache, error) {
	c := Cache{
		hostname: hostname,
		path:     path,
	}
	sk, err := signature.LoadSecretKey(secretKey)
	if err != nil {
		return c, fmt.Errorf("error loading the secret key: %w", err)
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
