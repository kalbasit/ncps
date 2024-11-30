package cache

import (
	"fmt"

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

type Cache struct {
	hostname  string
	path      string
	secretKey signature.SecretKey
}

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

func (c Cache) PublicKey() string {
	return c.secretKey.ToPublicKey().String()
}
