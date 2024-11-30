package upstreamcache

import (
	"fmt"

	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

type UpstreamCache struct {
	Host       string
	PublicKeys []signature.PublicKey
}

func New(host string, pubKeys []string) (UpstreamCache, error) {
	uc := UpstreamCache{Host: host}

	for _, pubKey := range pubKeys {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			return uc, fmt.Errorf("error parsing the public key: %w", err)
		}
		uc.PublicKeys = append(uc.PublicKeys, pk)
	}

	return uc, nil
}
