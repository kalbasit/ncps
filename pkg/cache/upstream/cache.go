package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

var (
	// ErrHostnameRequired is returned if the given hostName to New is not given.
	ErrHostnameRequired = errors.New("hostName is required")

	// ErrHostnameMustContainScheme is returned if the given hostName to New did not contain a scheme.
	ErrHostnameMustContainScheme = errors.New("url must contain scheme")

	// ErrHostnameNotValid is returned if the given hostName to New is not valid.
	ErrHostnameNotValid = errors.New("hostName is not valid")

	// ErrHostnameMustNotContainPath is returned if the given hostName to New contained a path.
	ErrHostnameMustNotContainPath = errors.New("hostName must not contain a path")

	// ErrNotFound is returned if the nar or narinfo were not found.
	ErrNotFound = errors.New("not found")

	// ErrUnexpectedHTTPStatusCode is returned if the response has an unexpected status code.
	ErrUnexpectedHTTPStatusCode = errors.New("unexpected HTTP status code")

	// ErrSignatureValidationFailed is returned if the signature validation of the narinfo has failed.
	ErrSignatureValidationFailed = errors.New("signature validation has failed")
)

// Cache represents the upstream cache service.
type Cache struct {
	url        *url.URL
	logger     log15.Logger
	priority   uint64
	publicKeys []signature.PublicKey
}

func New(logger log15.Logger, u *url.URL, pubKeys []string) (Cache, error) {
	c := Cache{logger: logger, url: u}

	if err := c.validateURL(u); err != nil {
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
		return c, fmt.Errorf("error parsing the priority for %q: %w", u, err)
	}

	c.priority = priority

	return c, nil
}

// GetHostname returns the hostname.
func (c Cache) GetHostname() string { return c.url.Hostname() }

// GetNarInfo returns a parsed NarInfo from the cache server.
func (c Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", c.url.JoinPath(helper.NarInfoURLPath(hash)).String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating a new request: %w", err)
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("error performing the request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}

		c.logger.Error(ErrUnexpectedHTTPStatusCode.Error(), "status_code", resp.StatusCode)

		return nil, ErrUnexpectedHTTPStatusCode
	}

	ni, err := narinfo.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	if err := ni.Check(); err != nil {
		return ni, fmt.Errorf("error while checking the narInfo: %w", err)
	}

	if !signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, c.publicKeys) {
		return ni, ErrSignatureValidationFailed
	}

	return ni, nil
}

// GetNar returns the NAR archive from the cache server.
// NOTE: It's the caller responsibility to close the body.
func (c Cache) GetNar(ctx context.Context, hash, compression string) (int64, io.ReadCloser, error) {
	log := c.logger.New("hash", hash, "compression", compression)

	r, err := http.NewRequestWithContext(ctx, "GET", c.url.JoinPath(helper.NarURLPath(hash, compression)).String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("error creating a new request: %w", err)
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, nil, fmt.Errorf("error performing the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusNotFound {
			return 0, nil, ErrNotFound
		}

		log.Error(ErrUnexpectedHTTPStatusCode.Error(), "status_code", resp.StatusCode)

		return 0, nil, ErrUnexpectedHTTPStatusCode
	}

	cls := resp.Header.Get("Content-Length")

	cl, err := strconv.ParseInt(cls, 10, 64)
	if err != nil {
		log.Error("error computing the content-length", "Content-Length", cls, "error", err)

		// TODO: Compute narinfo, pull it and return narInfo.FileSize
		return 0, resp.Body, nil
	}

	// TODO: Pull the narInfo and validate that narInfo.FileSize == cl
	return cl, resp.Body, nil
}

// GetPriority returns the priority of this upstream cache.
func (c Cache) GetPriority() uint64 { return c.priority }

func (c Cache) parsePriority() (uint64, error) {
	// TODO: Should probably pass context around and have things like logger in the context
	ctx := context.Background()

	ctx, cancelFn := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFn()

	r, err := http.NewRequestWithContext(ctx, "GET", c.url.JoinPath("/nix-cache-info").String(), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating a new request: %w", err)
	}

	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, fmt.Errorf("error performing the request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Error(ErrUnexpectedHTTPStatusCode.Error(), "status_code", resp.StatusCode)

		return 0, ErrUnexpectedHTTPStatusCode
	}

	nci, err := nixcacheinfo.Parse(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("error parsing the nix-cache-info: %w", err)
	}

	return nci.Priority, nil
}

func (c Cache) validateURL(u *url.URL) error {
	if u == nil {
		c.logger.Error("given url is nil", "url", u)

		return ErrHostnameRequired
	}

	if u.Scheme == "" {
		c.logger.Error("hostname should not contain a scheme", "url", u)

		return ErrHostnameMustContainScheme
	}

	if u.Path != "" {
		c.logger.Error("hostname should not contain a path", "url", u)

		return ErrHostnameMustNotContainPath
	}

	return nil
}
