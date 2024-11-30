package upstream

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

var (
	// ErrHostnameRequired is returned if the given hostName to New is not given
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustNotContainScheme is returned if the given hostName to New contained a scheme
	ErrHostnameMustNotContainScheme = errors.New("hostName must not contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")
)

// Cache represents the upstream cache service
type Cache struct {
	hostName   string
	logger     log15.Logger
	publicKeys []signature.PublicKey
}

func New(logger log15.Logger, hostName string, pubKeys []string) (Cache, error) {
	c := Cache{logger: logger, hostName: hostName}

	if err := c.validateHostname(hostName); err != nil {
		return c, err
	}

	for _, pubKey := range pubKeys {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			return c, fmt.Errorf("error parsing the public key: %w", err)
		}
		c.publicKeys = append(c.publicKeys, pk)
	}

	return c, nil
}

func (c Cache) validateHostname(hostName string) error {
	if hostName == "" {
		c.logger.Error("given hostname is empty", "hostName", hostName)
		return ErrHostnameRequired
	}

	u, err := url.Parse(hostName)
	if err != nil {
		c.logger.Error("failed to parse the hostname", "hostName", hostName, "error", err)
		return fmt.Errorf("error parsing the hostName %q: %w", hostName, err)
	}
	if u.Scheme != "" {
		c.logger.Error("hostname should not contain a scheme", "hostName", hostName, "scheme", u.Scheme)
		return ErrHostnameMustNotContainScheme
	}
	if strings.Contains(hostName, "/") {
		c.logger.Error("hostname should not contain a path", "hostName", hostName)
		return ErrHostnameMustNotContainPath
	}

	return nil
}
