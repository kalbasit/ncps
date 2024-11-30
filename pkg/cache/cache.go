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
func (c Cache) GetFile(key string) (io.ReadCloser, error) { return os.Open(path.Join(c.path, key)) }
