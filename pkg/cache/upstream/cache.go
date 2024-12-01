package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

var (
	// ErrHostnameRequired is returned if the given hostName to New is not given.
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustNotContainScheme is returned if the given hostName to New contained a scheme.
	ErrHostnameMustNotContainScheme = errors.New("hostName must not contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid.
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path.
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")

	// ErrUnexpectedHTTPStatusCode is returned if the response has an unexpected status code.
	ErrUnexpectedHTTPStatusCode = errors.New("unexpected HTTP status code")
)

// Cache represents the upstream cache service.
type Cache struct {
	hostName   string
	logger     log15.Logger
	priority   uint64
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

	priority, err := c.parsePriority()
	if err != nil {
		return c, fmt.Errorf("error parsing the priority: %w", err)
	}

	c.priority = priority

	return c, nil
}

// GetPriority returns the priority of this upstream cache.
func (c Cache) GetPriority() uint64 { return c.priority }

func (c Cache) parsePriority() (uint64, error) {
	// TODO: Should probably pass context around and have things like logger in the context
	ctx := context.Background()

	ctx, cancelFn := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFn()

	r, err := http.NewRequestWithContext(ctx, "GET", "https://"+c.hostName+"/nix-cache-info", nil)
	if err != nil {
		return 0, fmt.Errorf("error creating a new request: %w", err)
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, fmt.Errorf("error performing the request: %w", err)
	}

	// defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, ErrUnexpectedHTTPStatusCode
	}

	nci, err := nixcacheinfo.Parse(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("error parsing the nix-cache-info: %w", err)
	}

	return nci.Priority, nil
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
