package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"

	"github.com/kalbasit/ncps/pkg/helper"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/nixcacheinfo"
)

var (
	// ErrURLRequired is returned if the given URL to New is not given.
	ErrURLRequired = errors.New("the URL is required")

	// ErrURLMustContainScheme is returned if the given URL to New did not contain a scheme.
	ErrURLMustContainScheme = errors.New("the URL must contain scheme")

	// ErrInvalidURL is returned if the given hostName to New is not valid.
	ErrInvalidURL = errors.New("the URL is not valid")

	// ErrNotFound is returned if the nar or narinfo were not found.
	ErrNotFound = errors.New("not found")

	// ErrUnexpectedHTTPStatusCode is returned if the response has an unexpected status code.
	ErrUnexpectedHTTPStatusCode = errors.New("unexpected HTTP status code")

	// ErrSignatureValidationFailed is returned if the signature validation of the narinfo has failed.
	ErrSignatureValidationFailed = errors.New("signature validation has failed")
)

// Cache represents the upstream cache service.
type Cache struct {
	httpClient *http.Client
	url        *url.URL
	priority   uint64
	publicKeys []signature.PublicKey
}

func New(ctx context.Context, u *url.URL, pubKeys []string) (Cache, error) {
	c := Cache{}

	if u == nil {
		return c, ErrURLRequired
	}

	c.url = u

	ctx = zerolog.Ctx(ctx).
		With().
		Str("upstream-url", c.url.String()).
		Logger().
		WithContext(ctx)

	if err := c.validateURL(u); err != nil {
		return c, err
	}

	c.httpClient = &http.Client{
		Transport: &http.Transport{
			// Disable automatic decompression
			DisableCompression: true,
		},
	}

	for _, pubKey := range pubKeys {
		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			return c, fmt.Errorf("error parsing the public key: %w", err)
		}

		c.publicKeys = append(c.publicKeys, pk)
	}

	priority, err := c.parsePriority(ctx)
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
	u := c.url.JoinPath(helper.NarInfoURLPath(hash)).String()

	ctx = zerolog.Ctx(ctx).
		With().
		Str("narinfo-hash", hash).
		Str("narinfo-url", u).
		Str("upstream-url", c.url.String()).
		Logger().
		WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating a new request: %w", err)
	}

	zerolog.Ctx(ctx).
		Info().
		Msg("download the narinfo from upstream")

	resp, err := c.httpClient.Do(r)
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

		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

		return nil, ErrUnexpectedHTTPStatusCode
	}

	ni, err := narinfo.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing the narinfo: %w", err)
	}

	if err := ni.Check(); err != nil {
		return ni, fmt.Errorf("error while checking the narInfo: %w", err)
	}

	if len(c.publicKeys) > 0 {
		if !signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, c.publicKeys) {
			return ni, ErrSignatureValidationFailed
		}
	}

	return ni, nil
}

// GetNar returns the NAR archive from the cache server.
// NOTE: It's the caller responsibility to close the body.
func (c Cache) GetNar(ctx context.Context, narURL nar.URL, mutators ...func(*http.Request)) (*http.Response, error) {
	u := narURL.JoinURL(c.url).String()

	ctx = narURL.NewLogger(
		zerolog.Ctx(ctx).
			With().
			Str("nar-url", u).
			Str("upstream-url", c.url.String()).
			Logger(),
	).WithContext(ctx)

	r, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating a new request: %w", err)
	}

	for _, mutator := range mutators {
		mutator(r)
	}

	zerolog.Ctx(ctx).
		Info().
		Msg("download the nar from upstream")

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("error performing the request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		//nolint:errcheck
		io.Copy(io.Discard, resp.Body)

		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}

		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

		return nil, ErrUnexpectedHTTPStatusCode
	}

	return resp, nil
}

// GetPriority returns the priority of this upstream cache.
func (c Cache) GetPriority() uint64 { return c.priority }

func (c Cache) parsePriority(ctx context.Context) (uint64, error) {
	ctx, cancelFn := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFn()

	r, err := http.NewRequestWithContext(ctx, "GET", c.url.JoinPath("/nix-cache-info").String(), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating a new request: %w", err)
	}

	resp, err := c.httpClient.Do(r)
	if err != nil {
		return 0, fmt.Errorf("error performing the request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		zerolog.Ctx(ctx).
			Error().
			Err(ErrUnexpectedHTTPStatusCode).
			Int("status_code", resp.StatusCode).
			Send()

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
		return ErrURLRequired
	}

	if u.Scheme == "" {
		return ErrURLMustContainScheme
	}

	return nil
}
